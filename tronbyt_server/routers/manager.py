"""Manager router."""

import json
import logging
import secrets
import sqlite3
import string
import time
import uuid
from datetime import date, timedelta
from pathlib import Path
from random import randint
from typing import Annotated, Any, cast

from fastapi import (
    APIRouter,
    Body,
    Depends,
    File,
    Form,
    HTTPException,
    Request,
    UploadFile,
    status,
)
from fastapi.responses import FileResponse, RedirectResponse, Response, JSONResponse
from pydantic import BaseModel
from werkzeug.utils import secure_filename
from zoneinfo import available_timezones
from markupsafe import escape

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import Settings, get_settings
from tronbyt_server.dependencies import get_db, manager
from tronbyt_server.flash import flash
from tronbyt_server.models import (
    App,
    RecurrencePattern,
    RecurrenceType,
    Weekday,
    DEFAULT_DEVICE_TYPE,
    Device,
    DeviceID,
    DeviceType,
    Location,
    User,
)
from tronbyt_server.pixlet import call_handler, get_schema
from tronbyt_server.templates import templates
from tronbyt_server.utils import (
    push_brightness_test_image,
    possibly_render,
    render_app,
    send_default_image,
    send_image,
    server_root,
    set_repo,
    ws_root,
)

router = APIRouter(tags=["manager"])
logger = logging.getLogger(__name__)


def parse_time_input(time_str: str) -> str:
    """
    Parse time input in various formats and return as HH:MM string.

    Accepts:
    - HH:MM format (e.g., "22:00", "6:30")
    - HHMM format (e.g., "2200", "0630")
    - H:MM format (e.g., "6:30")
    - HMM format (e.g., "630")

    Returns:
    - Time string in HH:MM format

    Raises:
    - ValueError if the input is invalid
    """
    time_str = time_str.strip()

    if not time_str:
        raise ValueError("Time cannot be empty")

    try:
        # Try to parse as HH:MM or H:MM format
        if ":" in time_str:
            parts = time_str.split(":")
            if len(parts) != 2:
                raise ValueError(f"Invalid time format: {time_str}")
            hour_str, minute_str = parts
            hour = int(hour_str)
            minute = int(minute_str)
        else:
            # Parse as HHMM or HMM format
            if len(time_str) == 4:
                hour = int(time_str[:2])
                minute = int(time_str[2:])
            elif len(time_str) == 3:
                hour = int(time_str[0])
                minute = int(time_str[1:])
            elif len(time_str) == 2:
                hour = int(time_str)
                minute = 0
            elif len(time_str) == 1:
                hour = int(time_str)
                minute = 0
            else:
                raise ValueError(f"Invalid time format: {time_str}")
    except ValueError as e:
        # Re-raise with more context if it's a conversion error
        if "invalid literal" in str(e):
            raise ValueError(f"Time must contain only numbers: {time_str}")
        raise

    # Validate hour and minute
    if hour < 0 or hour > 23:
        raise ValueError(f"Hour must be between 0 and 23: {hour}")
    if minute < 0 or minute > 59:
        raise ValueError(f"Minute must be between 0 and 59: {minute}")

    return f"{hour:02d}:{minute:02d}"


def _next_app_logic(
    db_conn: sqlite3.Connection,
    device_id: DeviceID,
    last_app_index: int | None = None,
    recursion_depth: int = 0,
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    # first check for a pushed file starting with __ and just return that and then delete it.
    # This needs to happen before brightness check to clear any ephemeral images
    pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            print(f"Found pushed image {ephemeral_file}")
            response = send_image(ephemeral_file, device, None, True)
            ephemeral_file.unlink()
            return response

    # If brightness is 0, short-circuit and return default image to save processing
    brightness = device.brightness or 50
    if brightness == 0:
        logger.debug("Brightness is 0, returning default image")
        return send_default_image(device)

    if recursion_depth > len(device.apps):
        logger.warning("Maximum recursion depth exceeded, sending default image")
        return send_default_image(device)

    if last_app_index is None:
        last_app_index = db.get_last_app_index(db_conn, device_id)
        if last_app_index is None:
            raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    # if no apps return default image
    if not device.apps:
        return send_default_image(device)

    pinned_app_iname = device.pinned_app
    is_pinned_app = False
    is_night_mode_app = False
    if pinned_app_iname and pinned_app_iname in device.apps:
        logger.debug(f"Using pinned app: {pinned_app_iname}")
        app = device.apps[pinned_app_iname]
        is_pinned_app = True
        # For pinned apps, we don't update last_app_index since we're not cycling
    else:
        # Normal app selection logic
        apps_list = sorted(
            [app for app in device.apps.values()],
            key=lambda x: x.order,
        )
        is_night_mode_app = False
        if db.get_night_mode_is_active(device) and device.night_mode_app in device.apps:
            app = device.apps[device.night_mode_app]
            is_night_mode_app = True
        elif last_app_index + 1 < len(apps_list):
            app = apps_list[last_app_index + 1]
            last_app_index += 1
        else:
            app = apps_list[0]
            last_app_index = 0

    # For pinned apps, always display them regardless of enabled/schedule status
    # For other apps, check if they should be displayed
    if (
        not is_pinned_app
        and not is_night_mode_app
        and (not app.enabled or not db.get_is_app_schedule_active(app, device))
    ):
        return _next_app_logic(db_conn, device_id, last_app_index, recursion_depth + 1)

    if (
        not possibly_render(db_conn, user, device_id, app, logger)
        or app.empty_last_render
    ):
        return _next_app_logic(db_conn, device_id, last_app_index, recursion_depth + 1)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device_id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device_id) / f"{app.name}-{app.iname}.webp"

    if webp_path.exists() and webp_path.stat().st_size > 0:
        response = send_image(webp_path, device, app)
        db.save_last_app_index(db_conn, device_id, last_app_index)
        return response

    return _next_app_logic(db_conn, device_id, last_app_index, recursion_depth + 1)


@router.get("/", name="index")
def index(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Render the main page with a list of devices."""
    devices = []
    user_updated = False
    if user.devices:
        for device in reversed(list(user.devices.values())):
            if not device.ws_url:
                device.ws_url = ws_root() + f"/{device.id}/ws"
                user_updated = True

            ui_device = device.model_copy()
            if ui_device.brightness:
                ui_device.brightness = db.percent_to_ui_scale(ui_device.brightness)
            if ui_device.night_brightness:
                ui_device.night_brightness = db.percent_to_ui_scale(
                    ui_device.night_brightness
                )
            devices.append(ui_device)

        if user_updated:
            db.save_user(db_conn, user)

    return templates.TemplateResponse(
        request, "manager/index.html", {"devices": devices, "user": user}
    )


@router.get("/create", name="create")
def create(request: Request, user: User = Depends(manager)) -> Response:
    """Render the device creation page."""
    return templates.TemplateResponse(request, "manager/create.html", {"user": user})


class DeviceCreateFormData(BaseModel):
    """Represents the form data for creating a device."""

    name: str | None = None
    device_type: str = DEFAULT_DEVICE_TYPE
    img_url: str | None = None
    ws_url: str | None = None
    api_key: str | None = None
    notes: str | None = None
    brightness: int = 3
    location: str | None = None


@router.post("/create", name="create_post")
def create_post(
    request: Request,
    form_data: Annotated[DeviceCreateFormData, Form()],
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle device creation."""
    error = None
    if not form_data.name or db.get_device_by_name(user, form_data.name):
        error = "Unique name is required."
    if error is not None:
        flash(request, error)
        return templates.TemplateResponse(
            request,
            "manager/create.html",
            {"user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    max_attempts = 10
    for _ in range(max_attempts):
        device_id = str(uuid.uuid4())[0:8]
        if device_id not in user.devices:
            break
    else:
        flash(request, "Could not generate a unique device ID.")
        return templates.TemplateResponse(
            request,
            "manager/create.html",
            {"user": user},
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )

    img_url = form_data.img_url or f"{server_root()}/{device_id}/next"
    ws_url = form_data.ws_url or ws_root() + f"/{device_id}/ws"
    api_key = form_data.api_key or "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )

    percent_brightness = db.ui_scale_to_percent(form_data.brightness)

    device = Device(
        id=device_id,
        name=form_data.name or device_id,
        type=cast(DeviceType, form_data.device_type),
        img_url=img_url,
        ws_url=ws_url,
        api_key=api_key,
        brightness=percent_brightness,
        night_brightness=0,
        dim_brightness=None,
        default_interval=10,
        notes=form_data.notes or "",
    )

    if form_data.location and form_data.location != "{}":
        try:
            loc = json.loads(form_data.location)
            lat = loc.get("lat")
            lng = loc.get("lng")
            if lat and lng:
                device.location = Location(
                    locality=loc.get("locality", ""),
                    description=loc.get("description", ""),
                    place_id=loc.get("place_id", ""),
                    lat=lat,
                    lng=lng,
                    timezone=loc.get("timezone", ""),
                )
            else:
                flash(request, "Invalid location")
        except json.JSONDecodeError as e:
            flash(request, f"Location JSON error {e}")

    user.devices[device.id] = device
    if db.save_user(db_conn, user) and not db.get_device_webp_dir(device.id).is_dir():
        db.get_device_webp_dir(device.id).mkdir(parents=True)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/update", name="update")
def update(
    request: Request, device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    default_img_url = f"{server_root()}/{device_id}/next"
    default_ws_url = ws_root() + f"/{device_id}/ws"

    ui_device = device.model_copy()
    if ui_device.brightness:
        ui_device.brightness = db.percent_to_ui_scale(ui_device.brightness)
    if ui_device.night_brightness:
        ui_device.night_brightness = db.percent_to_ui_scale(ui_device.night_brightness)

    # Convert legacy integer time format to HH:MM for display
    if ui_device.night_start and isinstance(ui_device.night_start, int):
        ui_device.night_start = f"{ui_device.night_start:02d}:00"
    if ui_device.night_end and isinstance(ui_device.night_end, int):
        ui_device.night_end = f"{ui_device.night_end:02d}:00"

    return templates.TemplateResponse(
        request,
        "manager/update.html",
        {
            "device": ui_device,
            "available_timezones": available_timezones(),
            "default_img_url": default_img_url,
            "default_ws_url": default_ws_url,
            "user": user,
        },
    )


@router.post("/{device_id}/update_brightness", name="update_brightness")
def update_brightness(
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    brightness: int = Form(...),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    # Convert UI scale (0-5) to percentage (0-100) and store only the percentage
    # Validate brightness is in range 0-5
    if brightness < 0 or brightness > 5:
        return Response(
            status_code=status.HTTP_400_BAD_REQUEST,
            content="Brightness must be between 0 and 5",
        )

    device.brightness = db.ui_scale_to_percent(brightness)
    db.save_user(db_conn, user)

    # Push an ephemeral brightness test image to the device (but not when brightness is 0)
    if brightness > 0:
        try:
            push_brightness_test_image(device_id, logger)
        except Exception as e:
            logger.error(f"Failed to push brightness test image: {e}")
            # Don't fail the brightness update if the test image fails

    return Response(status_code=status.HTTP_200_OK)


@router.post("/{device_id}/update_interval", name="update_interval")
def update_interval(
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    interval: int = Form(...),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    device.default_interval = interval
    db.save_user(db_conn, user)
    return Response(status_code=status.HTTP_200_OK)


class DeviceUpdateFormData(BaseModel):
    """Represents the form data for updating a device."""

    name: str
    device_type: str
    img_url: str | None = None
    ws_url: str | None = None
    api_key: str | None = None
    notes: str | None = None
    brightness: int
    night_brightness: int
    default_interval: int
    night_mode_enabled: bool = False
    night_start: str
    night_end: str
    night_mode_app: str | None = None
    dim_time: str | None = None
    dim_brightness: int | None = None
    timezone: str | None = None
    location: str | None = None


@router.post("/{device_id}/update", name="update_post")
def update_post(
    request: Request,
    device_id: DeviceID,
    form_data: Annotated[DeviceUpdateFormData, Form()],
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle device update."""

    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    error = None
    if not form_data.name or not device_id:
        error = "Id and Name is required."
    if error is not None:
        flash(request, error)
        return RedirectResponse(
            url=f"/{device_id}/update", status_code=status.HTTP_302_FOUND
        )

    device.name = form_data.name
    device.type = cast(DeviceType, form_data.device_type)
    device.img_url = (
        db.sanitize_url(form_data.img_url)
        if form_data.img_url
        else f"{server_root()}/{device_id}/next"
    )
    device.ws_url = (
        db.sanitize_url(form_data.ws_url)
        if form_data.ws_url
        else ws_root() + f"/{device_id}/ws"
    )
    device.api_key = form_data.api_key or ""
    device.notes = form_data.notes or ""
    device.brightness = db.ui_scale_to_percent(form_data.brightness)
    device.night_brightness = db.ui_scale_to_percent(form_data.night_brightness)
    device.default_interval = form_data.default_interval
    device.night_mode_enabled = form_data.night_mode_enabled
    device.night_mode_app = form_data.night_mode_app or ""
    device.timezone = form_data.timezone

    if form_data.night_start:
        try:
            device.night_start = parse_time_input(form_data.night_start)
        except ValueError as e:
            flash(request, f"Invalid night start time: {e}")

    if form_data.night_end:
        try:
            device.night_end = parse_time_input(form_data.night_end)
        except ValueError as e:
            flash(request, f"Invalid night end time: {e}")

    # Handle dim time and dim brightness
    # Note: Dim mode ends at night_end time (if set) or 6:00 AM by default
    if form_data.dim_time and form_data.dim_time.strip():
        try:
            device.dim_time = parse_time_input(form_data.dim_time)
        except ValueError as e:
            flash(request, f"Invalid dim time: {e}")
    elif device.dim_time:
        # Remove dim_time if the field is empty
        device.dim_time = None

    if form_data.dim_brightness:
        device.dim_brightness = db.ui_scale_to_percent(form_data.dim_brightness)

    if form_data.location and form_data.location != "{}":
        try:
            loc = json.loads(form_data.location)
            lat = loc.get("lat")
            lng = loc.get("lng")
            if lat and lng:
                device.location = Location(
                    locality=loc.get("locality", ""),
                    description=loc.get("description", ""),
                    place_id=loc.get("place_id", ""),
                    lat=lat,
                    lng=lng,
                    timezone=loc.get("timezone", ""),
                )
            else:
                flash(request, "Invalid location")
        except json.JSONDecodeError as e:
            flash(request, f"Location JSON error {e}")

    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/delete", name="delete")
def delete(
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    user.devices.pop(device_id)
    db.save_user(db_conn, user)
    db.delete_device_dirs(device_id)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/addapp", name="addapp")
def addapp(
    request: Request,
    device_id: DeviceID,
    user: User = Depends(manager),
    settings: Settings = Depends(get_settings),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    custom_apps_list = db.get_apps_list(user.username)
    apps_list = db.get_apps_list("system")

    installed_app_names = {
        app.name for dev in user.devices.values() for app in dev.apps.values()
    }

    # Mark installed apps and sort so that installed apps appear first
    for app_metadata in apps_list:
        app_metadata["is_installed"] = app_metadata["name"] in installed_app_names

    # Also mark installed status for custom apps
    for app_metadata in custom_apps_list:
        if "name" in app_metadata:
            app_metadata["is_installed"] = app_metadata["name"] in installed_app_names

    # Sort apps_list so that installed apps appear first
    apps_list.sort(key=lambda app_metadata: not app_metadata["is_installed"])

    system_repo_info = system_apps.get_system_repo_info(db.get_data_dir())

    return templates.TemplateResponse(
        request,
        "manager/addapp.html",
        {
            "device": device,
            "apps_list": apps_list,
            "custom_apps_list": custom_apps_list,
            "system_repo_info": system_repo_info,
            "user": user,
            "settings": settings,
        },
    )


@router.post("/{device_id}/addapp", name="addapp_post")
def addapp_post(
    request: Request,
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    name: str = Form(...),
    uinterval: int | None = Form(None),
    display_time: int | None = Form(None),
    notes: str | None = Form(None),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    if not name:
        flash(request, "App name required.")
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    max_attempts = 10
    for _ in range(max_attempts):
        iname = str(randint(100, 999))
        if iname not in device.apps:
            break
    else:
        flash(request, "Could not generate a unique installation ID.")
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    app_details = db.get_app_details_by_name(db_conn, user.username, name)

    app = App(
        id=app_details.get("id", ""),
        name=name,
        iname=iname,
        path=app_details.get("path", ""),
        enabled=False,
        last_render=0,
        uinterval=(
            uinterval
            if uinterval is not None
            else app_details.get("recommended_interval", 0)
        ),
        display_time=display_time if display_time is not None else 0,
        notes=notes or "",
        order=len(device.apps),
    )

    device.apps[iname] = app
    db.save_user(db_conn, user)

    return RedirectResponse(
        url=f"{request.url_for('configapp', device_id=device_id, iname=iname)}?delete_on_cancel=true",
        status_code=status.HTTP_302_FOUND,
    )


@router.get("/{device_id}/uploadapp", name="uploadapp")
def uploadapp(
    request: Request, device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    user_apps_path = db.get_users_dir() / user.username / "apps"
    user_apps_path.mkdir(parents=True, exist_ok=True)
    star_files = [file.name for file in user_apps_path.rglob("*.star")]
    return templates.TemplateResponse(
        request,
        "manager/uploadapp.html",
        {"files": star_files, "device_id": device_id, "user": user},
    )


@router.post("/{device_id}/uploadapp", name="app_preview")
async def uploadapp_post(
    request: Request,
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    user_apps_path = db.get_users_dir() / user.username / "apps"
    if not file.filename:
        flash(request, "No file")
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    filename = secure_filename(file.filename)
    app_name = Path(filename).stem
    app_subdir = user_apps_path / app_name
    app_subdir.mkdir(parents=True, exist_ok=True)

    try:
        app_subdir.relative_to(db.get_users_dir())
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in apps_path")
        flash(request, "Invalid file path")
        return templates.TemplateResponse(
            request,
            "manager/uploadapp.html",
            {"device_id": device_id, "user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    if not await db.save_user_app(file, app_subdir):
        flash(request, "File type not allowed")
        return templates.TemplateResponse(
            request,
            "manager/uploadapp.html",
            {"device_id": device_id, "user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    flash(request, "Upload Successful")
    preview = db.get_data_dir() / "apps" / f"{app_name}.webp"
    render_app(
        db_conn,
        app_subdir,
        {},
        preview,
        Device(
            id="aaaaaaaa",
            brightness=100,
            night_brightness=0,
            dim_brightness=None,
            default_interval=15,
        ),
        None,
        logger,
    )

    return RedirectResponse(
        url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
    )


@router.get("/app_preview/{filename}", name="app_preview")
def app_preview(filename: str) -> FileResponse:
    """Serve app preview images."""
    file_path = db.get_data_dir() / "apps" / filename
    if not file_path.is_file():
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    return FileResponse(file_path)


@router.get("/{device_id}/deleteupload/{filename}", name="deleteupload")
def deleteupload(
    request: Request,
    device_id: DeviceID,
    filename: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    if not db.get_device_by_id(db_conn, device_id):
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )
    if any(
        app.path and Path(app.path).name == filename
        for device in user.devices.values()
        for app in device.apps.values()
    ):
        flash(
            request,
            f"Cannot delete {filename} because it is installed on a device.",
        )
    else:
        db.delete_user_upload(user, filename)

    return RedirectResponse(
        url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
    )


@router.get("/{device_id}/{iname}/delete", name="deleteapp")
@router.post("/{device_id}/{iname}/delete", name="deleteapp")
def deleteapp(
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device.id) / "pushed" / f"{app.name}.webp"
    else:
        webp_path = db.get_device_webp_dir(device.id) / f"{app.name}-{app.iname}.webp"

    if webp_path.is_file():
        webp_path.unlink()

    device.apps.pop(iname)
    db.save_user(db_conn, user)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/toggle_pin", name="toggle_pin")
def toggle_pin(
    request: Request,
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return Response(
            status_code=status.HTTP_404_NOT_FOUND, content="Device not found"
        )

    if iname not in device.apps:
        return Response(status_code=status.HTTP_404_NOT_FOUND, content="App not found")

    # Check if this app is currently pinned
    if getattr(device, "pinned_app", None) == iname:
        device.pinned_app = ""
        flash(request, "App unpinned.")
    else:
        device.pinned_app = iname
        flash(request, "App pinned.")

    db.save_user(db_conn, user)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/updateapp", name="updateapp")
def updateapp(
    request: Request, device_id: DeviceID, iname: str, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    # Set default dates if not already set
    today = date.today()
    if not app.recurrence_start_date:
        app.recurrence_start_date = today
    if not app.recurrence_end_date:
        app.recurrence_end_date = today + timedelta(days=7)

    return templates.TemplateResponse(
        request,
        "manager/updateapp.html",
        {
            "app": app,
            "device": device,
            "device_id": device_id,
            "config": json.dumps(app.config, indent=4),
            "user": user,
        },
    )


class AppUpdateFormData(BaseModel):
    """Represents the form data for updating an app."""

    name: str
    uinterval: int | None = None
    display_time: int = 0
    notes: str | None = None
    enabled: bool = False
    start_time: str | None = None
    end_time: str | None = None
    days: list[str] = []
    use_custom_recurrence: bool = False
    recurrence_type: str | None = None
    recurrence_interval: int | None = None
    recurrence_start_date: str | None = None
    recurrence_end_date: str | None = None
    weekdays: list[str] = []
    monthly_pattern: str | None = None
    day_of_month: int | None = None
    day_of_week_pattern: str | None = None


@router.post("/{device_id}/{iname}/updateapp", name="updateapp_post")
def updateapp_post(
    request: Request,
    device_id: DeviceID,
    iname: str,
    form_data: Annotated[AppUpdateFormData, Form()],
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle app update."""

    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    recurrence_pattern = RecurrencePattern()
    if form_data.use_custom_recurrence:
        if form_data.recurrence_type == "weekly":
            recurrence_pattern.weekdays = [
                cast(Weekday, day) for day in form_data.weekdays
            ]
        elif form_data.recurrence_type == "monthly":
            if form_data.monthly_pattern == "day_of_month":
                recurrence_pattern.day_of_month = form_data.day_of_month
            elif form_data.monthly_pattern == "day_of_week":
                recurrence_pattern.day_of_week = form_data.day_of_week_pattern

    update_data: dict[str, Any] = {
        "name": form_data.name,
        "uinterval": form_data.uinterval or 0,
        "display_time": form_data.display_time,
        "notes": form_data.notes or "",
        "enabled": form_data.enabled,
        "start_time": form_data.start_time,
        "end_time": form_data.end_time,
        "days": form_data.days,
        "use_custom_recurrence": form_data.use_custom_recurrence,
        "recurrence_type": cast(RecurrenceType, form_data.recurrence_type or "daily"),
        "recurrence_interval": form_data.recurrence_interval or 1,
        "recurrence_pattern": recurrence_pattern,
    }
    if form_data.recurrence_start_date:
        update_data["recurrence_start_date"] = form_data.recurrence_start_date
    if form_data.recurrence_end_date:
        update_data["recurrence_end_date"] = form_data.recurrence_end_date

    if not form_data.name:
        flash(request, "Name is required.")
        temp_app = app.model_copy(update=update_data)
        return templates.TemplateResponse(
            request,
            "manager/updateapp.html",
            {
                "app": temp_app,
                "device_id": device_id,
                "config": json.dumps(temp_app.config, indent=4),
                "user": user,
            },
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    for key, value in update_data.items():
        setattr(app, key, value)

    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/toggle_enabled", name="toggle_enabled")
def toggle_enabled(
    request: Request,
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    app.enabled = not app.enabled
    db.save_user(db_conn, user)
    flash(request, "Changes saved.")
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/moveapp", name="moveapp")
def moveapp(
    request: Request,
    device_id: DeviceID,
    iname: str,
    direction: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    if direction not in ["up", "down"]:
        flash(request, "Invalid direction.")
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    apps_list = sorted(device.apps.values(), key=lambda x: x.order)
    current_idx = -1
    for i, app in enumerate(apps_list):
        if app.iname == iname:
            current_idx = i
            break

    if current_idx == -1:
        flash(request, "App not found.")
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    if direction == "up":
        if current_idx == 0:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        target_idx = current_idx - 1
    else:
        if current_idx == len(apps_list) - 1:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        target_idx = current_idx + 1

    apps_list[current_idx], apps_list[target_idx] = (
        apps_list[target_idx],
        apps_list[current_idx],
    )

    for i, app in enumerate(apps_list):
        app.order = i

    db.save_user(db_conn, user)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/configapp", name="configapp")
def configapp(
    request: Request,
    device_id: DeviceID,
    iname: str,
    delete_on_cancel: bool = False,
    user: User = Depends(manager),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app or not app.path:
        flash(request, "Error saving app, please try again.")
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    schema_json = get_schema(Path(app.path), logger)
    schema = json.loads(schema_json) if schema_json else None
    return templates.TemplateResponse(
        request,
        "manager/configapp.html",
        {
            "app": app,
            "device": device,
            "delete_on_cancel": delete_on_cancel,
            "config": app.config,
            "schema": schema,
            "user": user,
        },
    )


@router.post("/{device_id}/{iname}/configapp", name="preview")
async def configapp_post(
    request: Request,
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: Any = Body(...),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app or not app.path:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    app.config = config
    db.save_user(db_conn, user)

    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app.name}-{app.iname}.webp"
    image = render_app(
        db_conn,
        Path(app.path),
        config,
        webp_path,
        device,
        app,
        logger,
    )
    if image is not None:
        app.enabled = True
        app.last_render = int(time.time())
        db.save_app(db_conn, device_id, app)
    else:
        flash(request, "Error Rendering App")

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/preview", name="preview")
def preview(
    request: Request,
    device_id: DeviceID,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: str = "{}",
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")

    app = device.apps.get(iname)
    if not app or not app.path:
        raise HTTPException(status_code=404, detail="App not found")

    try:
        config_data = json.loads(config)
    except json.JSONDecodeError:
        raise HTTPException(status_code=400, detail="Invalid config JSON")

    app_path = Path(app.path)
    if not app_path.exists():
        raise HTTPException(status_code=404, detail="App path not found")

    try:
        data = render_app(
            db_conn=db_conn,
            app_path=app_path,
            config=config_data,
            webp_path=None,
            device=device,
            app=app,
            logger=logger,
        )
        if data is None:
            raise HTTPException(
                status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
                detail="Error running pixlet render",
            )

        return Response(content=data, media_type="image/webp")
    except Exception as e:
        logger.error(f"Error in preview: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Error generating preview",
        )


@router.get("/adminindex", name="adminindex")
def adminindex(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Render the admin index page."""
    if user.username != "admin":
        raise HTTPException(status_code=403, detail="Forbidden")
    users = db.get_all_users(db_conn)
    return templates.TemplateResponse(
        request, "manager/adminindex.html", {"users": users, "user": user}
    )


@router.post("/admin/{username}/deleteuser", name="deleteuser")
def deleteuser(
    username: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle user deletion by an admin."""
    if user.username != "admin":
        raise HTTPException(status_code=403, detail="Forbidden")
    if username != "admin":
        db.delete_user(db_conn, username)
    return RedirectResponse(url="/adminindex", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/firmware", name="generate_firmware")
def generate_firmware(
    request: Request, device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")

    firmware_version = db.get_firmware_version()
    return templates.TemplateResponse(
        request,
        "manager/firmware.html",
        {"device": device, "firmware_version": firmware_version, "user": user},
    )


@router.post("/{device_id}/firmware", name="generate_firmware_post")
def generate_firmware_post(
    device_id: DeviceID,
    user: User = Depends(manager),
    wifi_ap: str = Form(...),
    wifi_password: str = Form(...),
    img_url: str = Form(...),
    swap_colors: bool = Form(False),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")

    try:
        firmware_data = firmware_utils.generate_firmware(
            url=img_url,
            ap=wifi_ap,
            pw=wifi_password,
            device_type=device.type,
            swap_colors=swap_colors,
            logger=logger,
        )
    except Exception as e:
        logger.error(f"Error generating firmware: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Firmware generation failed",
        )

    return Response(
        content=firmware_data,
        media_type="application/octet-stream",
        headers={
            "Content-Disposition": f"attachment;filename=firmware_{device.type}_{device_id}.bin"
        },
    )


@router.post("/set_user_repo", name="set_user_repo")
def set_user_repo(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    app_repo_url: str = Form(...),
) -> Response:
    """Set the user's custom app repository."""
    apps_path = db.get_users_dir() / user.username / "apps"

    # Ensure apps_path is within the expected users directory to prevent path traversal
    try:
        apps_path.relative_to(db.get_users_dir())
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in apps_path")
        return Response(
            status_code=status.HTTP_400_BAD_REQUEST, content="Invalid repository path"
        )

    if set_repo(db_conn, request, user, "app_repo_url", apps_path, app_repo_url):
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.post("/set_api_key", name="set_api_key")
def set_api_key(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    api_key: str = Form(...),
) -> Response:
    """Set the user's API key."""
    if not api_key:
        flash(request, "API Key cannot be empty.")
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    user.api_key = api_key
    db.save_user(db_conn, user)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/set_system_repo", name="set_system_repo")
def set_system_repo(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    app_repo_url: str = Form(...),
) -> Response:
    """Set the system app repository (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=403, detail="Forbidden")
    if set_repo(
        db_conn,
        request,
        user,
        "system_repo_url",
        db.get_data_dir() / "system-apps",
        app_repo_url,
    ):
        system_apps.update_system_repo(db.get_data_dir(), logger)
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.post("/refresh_system_repo", name="refresh_system_repo")
def refresh_system_repo(
    request: Request,
    user: User = Depends(manager),
) -> Response:
    """Refresh the system app repository (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=403, detail="Forbidden")
    # Directly update the system repo - it handles git pull internally
    system_apps.update_system_repo(db.get_data_dir(), logger)
    flash(request, "System repo updated successfully")
    return RedirectResponse(
        url=request.url_for("index"), status_code=status.HTTP_302_FOUND
    )


@router.post("/mark_app_broken/{app_name}", name="mark_app_broken")
def mark_app_broken(
    app_name: str,
    user: User = Depends(manager),
    settings: Settings = Depends(get_settings),
) -> Response:
    """Mark an app as broken by adding it to broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if settings.PRODUCTION != "0":
        return JSONResponse(
            content={"success": False, "message": "Only available in development mode"},
            status_code=status.HTTP_403_FORBIDDEN,
        )

    # Only allow admin users
    if user.username != "admin":
        return JSONResponse(
            content={"success": False, "message": "Admin access required"},
            status_code=status.HTTP_403_FORBIDDEN,
        )

    try:
        # Get the broken_apps.txt path
        broken_apps_path = db.get_data_dir() / "system-apps" / "broken_apps.txt"

        # Read existing broken apps
        broken_apps = []
        if broken_apps_path.exists():
            broken_apps = broken_apps_path.read_text().splitlines()

        # Add .star extension if not present
        app_filename = app_name if app_name.endswith(".star") else f"{app_name}.star"

        # Check if already in the list
        if app_filename in broken_apps:
            return JSONResponse(
                content={
                    "success": False,
                    "message": "App is already marked as broken",
                },
                status_code=status.HTTP_400_BAD_REQUEST,
            )

        # Add to the list
        broken_apps.append(app_filename)

        # Write back to file
        broken_apps_path.write_text("\n".join(sorted(broken_apps)) + "\n")

        # Regenerate the apps.json to include the broken flag (without doing git pull)
        system_apps.generate_apps_json(db.get_data_dir(), logger)

        logger.info(f"Marked {app_filename} as broken")
        return JSONResponse(
            content={
                "success": True,
                "message": f"Added {escape(app_filename)} to broken_apps.txt",
            },
            status_code=status.HTTP_200_OK,
        )

    except Exception as e:
        logger.error(f"Error marking app as broken: {e}")
        return JSONResponse(
            content={"success": False, "message": str(e)},
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )


@router.post("/unmark_app_broken/{app_name}", name="unmark_app_broken")
def unmark_app_broken(
    app_name: str,
    user: User = Depends(manager),
    settings: Settings = Depends(get_settings),
) -> Response:
    """Remove an app from broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if settings.PRODUCTION != "0":
        return JSONResponse(
            content={"success": False, "message": "Only available in development mode"},
            status_code=status.HTTP_403_FORBIDDEN,
        )

    # Only allow admin users
    if user.username != "admin":
        return JSONResponse(
            content={"success": False, "message": "Admin access required"},
            status_code=status.HTTP_403_FORBIDDEN,
        )

    try:
        # Get the broken_apps.txt path
        broken_apps_path = db.get_data_dir() / "system-apps" / "broken_apps.txt"

        # Read existing broken apps
        if not broken_apps_path.exists():
            return JSONResponse(
                content={"success": False, "message": "No broken apps file found"},
                status_code=status.HTTP_404_NOT_FOUND,
            )

        broken_apps = broken_apps_path.read_text().splitlines()

        # Add .star extension if not present
        app_filename = app_name if app_name.endswith(".star") else f"{app_name}.star"

        # Check if in the list
        if app_filename not in broken_apps:
            return JSONResponse(
                content={"success": False, "message": "App is not marked as broken"},
                status_code=status.HTTP_400_BAD_REQUEST,
            )

        # Remove from the list
        broken_apps.remove(app_filename)

        # Write back to file
        if broken_apps:
            broken_apps_path.write_text("\n".join(sorted(broken_apps)) + "\n")
        else:
            # If no broken apps left, write empty file
            broken_apps_path.write_text("")

        # Regenerate the apps.json to remove the broken flag (without doing git pull)
        system_apps.generate_apps_json(db.get_data_dir(), logger)

        logger.info(f"Unmarked {app_filename} as broken")
        return JSONResponse(
            content={
                "success": True,
                "message": f"Removed {escape(app_filename)} from broken_apps.txt",
            },
            status_code=status.HTTP_200_OK,
        )

    except Exception as e:
        logger.error(f"Error unmarking app: {e}")
        return JSONResponse(
            content={"success": False, "message": str(e)},
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )


@router.post("/update_firmware", name="update_firmware")
def update_firmware(request: Request, user: User = Depends(manager)) -> Response:
    """Update firmware binaries (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=403, detail="Forbidden")
    try:
        result = firmware_utils.update_firmware_binaries(db.get_data_dir(), logger)
        if result["success"]:
            if result["action"] == "updated":
                flash(request, f"✅ {result['message']}", "success")
            elif result["action"] == "skipped":
                flash(request, f"ℹ️ {result['message']}", "info")
        else:
            flash(request, f"❌ {result['message']}", "error")
    except Exception as e:
        logger.error(f"Error updating firmware: {e}")
        flash(request, f"❌ Firmware update failed: {str(e)}", "error")
    return RedirectResponse(
        url="/auth/edit#firmware-management",
        status_code=status.HTTP_302_FOUND,
    )


@router.post("/refresh_user_repo", name="refresh_user_repo")
def refresh_user_repo(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Refresh the user's custom app repository."""
    apps_path = db.get_users_dir() / user.username / "apps"
    if set_repo(db_conn, request, user, "app_repo_url", apps_path, user.app_repo_url):
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.get("/export_user_config", name="export_user_config")
def export_user_config(user: User = Depends(manager)) -> Response:
    """Export user configuration as a JSON file."""
    user_dict = user.model_dump()
    user_dict.pop("password", None)
    user_json = json.dumps(user_dict, indent=4)
    return Response(
        content=user_json,
        media_type="application/json",
        headers={
            "Content-Disposition": f"attachment;filename={user.username}_config.json"
        },
    )


@router.get("/{device_id}/export_config", name="export_device_config")
def export_device_config(
    device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")
    device_json = json.dumps(device.model_dump(), indent=4)
    return Response(
        content=device_json,
        media_type="application/json",
        headers={"Content-Disposition": f"attachment;filename={device_id}_config.json"},
    )


@router.get("/{device_id}/import_config", name="import_device_config")
def import_device_config(
    request: Request, device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    return templates.TemplateResponse(
        request,
        "manager/import_config.html",
        {"device_id": device_id, "user": user},
    )


@router.post("/{device_id}/import_config", name="import_device")
async def import_device_config_post(
    request: Request,
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    if not file.filename:
        flash(request, "No selected file")
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )
    if not file.filename.endswith(".json"):
        flash(request, "Invalid file type. Please upload a JSON file.")
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )

    try:
        contents = await file.read()
        device_config = json.loads(contents)
        if not isinstance(device_config, dict):
            flash(request, "Invalid JSON structure")
            return RedirectResponse(
                url=f"/{device_id}/import_config",
                status_code=status.HTTP_302_FOUND,
            )
        if device_config["id"] != device_id:
            flash(request, "Not the same device id. Import skipped.")
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

        device_config["img_url"] = f"{server_root()}/{device_id}/next"
        user.devices[device_config["id"]] = Device(**device_config)
        db.save_user(db_conn, user)
        flash(request, "Device configuration imported successfully")
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        flash(request, f"Error parsing JSON file: {e}")
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )


@router.post("/import_user_config", name="import_device")
async def import_user_config(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    """Handle import of user configuration."""
    if not file.filename:
        flash(request, "No selected file")
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    if not file.filename.endswith(".json"):
        flash(request, "Invalid file type. Please upload a JSON file.")
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)

    try:
        contents = await file.read()
        user_config = json.loads(contents)
        if not isinstance(user_config, dict):
            flash(request, "Invalid JSON structure")
            return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)

        password = user.password
        user_config["password"] = password
        new_user = User(**user_config)
        db.save_user(db_conn, new_user)
        flash(request, "User configuration imported successfully")
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        flash(request, f"Error parsing JSON file: {e}")
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.get("/import_device", name="import_device")
def import_device(request: Request, user: User = Depends(manager)) -> Response:
    """Render the import device page."""
    return templates.TemplateResponse(
        request, "manager/import_config.html", {"user": user}
    )


@router.post("/import_device", name="next_app")
async def import_device_post(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    """Handle import of a new device."""
    if not file.filename:
        flash(request, "No selected file")
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)
    if not file.filename.endswith(".json"):
        flash(request, "Invalid file type. Please upload a JSON file.")
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)

    try:
        contents = await file.read()
        device_config = json.loads(contents)
        if not isinstance(device_config, dict):
            flash(request, "Invalid JSON structure")
            return RedirectResponse(
                url="/import_device", status_code=status.HTTP_302_FOUND
            )

        device_id = device_config.get("id")
        if not device_id:
            flash(request, "Device ID missing in config.")
            return RedirectResponse(
                url="/import_device", status_code=status.HTTP_302_FOUND
            )
        if device_id in user.devices or db.get_device_by_id(db_conn, device_id):
            flash(request, "Device already exists. Import skipped.")
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

        device_config["img_url"] = f"{server_root()}/{device_id}/next"
        device = Device(**device_config)
        user.devices[device.id] = device
        db.save_user(db_conn, user)
        flash(request, "Device configuration imported successfully")
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        flash(request, f"Error parsing JSON file: {e}")
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/next", name="next_app")
def next_app(
    device_id: DeviceID, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    return _next_app_logic(db_conn, device_id)


@router.get("/{device_id}/brightness", name="get_brightness")
def get_brightness(
    device_id: DeviceID, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    brightness_value = db.get_device_brightness_8bit(device)
    return Response(content=str(brightness_value), media_type="text/plain")


@router.get("/{device_id}/currentapp", name="currentwebp")
def currentwebp(
    device_id: DeviceID, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    apps_list = sorted(
        list(device.apps.values()),
        key=lambda x: x.order,
    )
    if not apps_list:
        return send_default_image(device)
    current_app_index = db.get_last_app_index(db_conn, device_id) or 0
    if current_app_index >= len(apps_list):
        current_app_index = 0
    current_app_iname = apps_list[current_app_index].iname
    return appwebp(device_id, current_app_iname, db_conn)


@router.get("/{device_id}/{iname}/appwebp", name="appwebp")
def appwebp(
    device_id: DeviceID, iname: str, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device_id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device_id) / f"{app.name}-{app.iname}.webp"
    if webp_path.exists() and webp_path.stat().st_size > 0:
        return send_image(webp_path, device, app)
    else:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)


@router.post("/{device_id}/{iname}/schema_handler/{handler}", name="schema_handler")
async def schema_handler(
    request: Request,
    device_id: DeviceID,
    iname: str,
    handler: str,
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app or not app.path:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    try:
        data = await request.json()
        if "param" not in data:
            raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST)

        result = call_handler(Path(app.path), handler, data["param"], logger)
        return Response(content=result, media_type="application/json")
    except Exception as e:
        logger.error(f"Error in schema_handler: {e}")
        raise HTTPException(status_code=status.HTTP_500_INTERNAL_SERVER_ERROR)


@router.get("/health", name="health")
def health() -> Response:
    """Health check endpoint."""
    return Response(content="OK", status_code=status.HTTP_200_OK)
