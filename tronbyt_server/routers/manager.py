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
from typing import Any

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
from werkzeug.utils import secure_filename
from zoneinfo import available_timezones
from markupsafe import escape

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import settings
from tronbyt_server.dependencies import get_db, manager
from tronbyt_server.flash import flash
from tronbyt_server.models.app import App, RecurrencePattern
from tronbyt_server.models.device import (
    DEFAULT_DEVICE_TYPE,
    Device,
    Location,
)
from tronbyt_server.models.user import User
from tronbyt_server.pixlet import call_handler, get_schema
from tronbyt_server.templates import templates
from tronbyt_server.utils import (
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


def _next_app_logic(
    db_conn: sqlite3.Connection,
    device_id: str,
    last_app_index: int | None = None,
    recursion_depth: int = 0,
) -> Response:
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    # If brightness is 0, short-circuit and return default image to save processing
    brightness = device.brightness or 50
    if brightness == 0:
        logger.debug("Brightness is 0, returning default image")
        return send_default_image(device)

    pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            print(f"Found pushed image {ephemeral_file}")
            response = send_image(ephemeral_file, device, None, True)
            ephemeral_file.unlink()
            return response

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


@router.get("/")
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


@router.get("/create")
def create(request: Request, user: User = Depends(manager)) -> Response:
    """Render the device creation page."""
    return templates.TemplateResponse(request, "manager/create.html", {"user": user})


@router.post("/create")
def create_post(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    name: str | None = Form(None),
    device_type: str = Form(DEFAULT_DEVICE_TYPE),
    img_url: str | None = Form(None),
    ws_url: str | None = Form(None),
    api_key: str | None = Form(None),
    notes: str | None = Form(None),
    brightness: int = Form(3),
    location: str | None = Form(None),
) -> Response:
    """Handle device creation."""
    error = None
    if not name or db.get_device_by_name(user, name):
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

    if not img_url:
        img_url = f"{server_root()}/{device_id}/next"
    if not ws_url:
        ws_url = ws_root() + f"/{device_id}/ws"

    if not api_key:
        api_key = "".join(
            secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
        )

    percent_brightness = db.ui_scale_to_percent(brightness)

    device = Device(
        id=device_id,
        name=name or device_id,
        type=device_type,
        img_url=img_url,
        ws_url=ws_url,
        api_key=api_key,
        brightness=percent_brightness,
        default_interval=10,
        notes=notes or "",
    )

    if location and location != "{}":
        try:
            loc = json.loads(location)
            lat = loc.get("lat")
            lng = loc.get("lng")
            if lat and lng:
                device.location = Location(
                    lat=lat,
                    lng=lng,
                    name=loc.get("name", ""),
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


@router.get("/{device_id}/update")
def update(request: Request, device_id: str, user: User = Depends(manager)) -> Response:
    """Render the device update page."""
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


@router.post("/{device_id}/update_brightness")
def update_brightness(
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    brightness: int = Form(...),
) -> Response:
    """Update the brightness for a device."""
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
    return Response(status_code=status.HTTP_200_OK)


@router.post("/{device_id}/update_interval")
def update_interval(
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    interval: int = Form(...),
) -> Response:
    """Update the interval for a device."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    device.default_interval = interval
    db.save_user(db_conn, user)
    return Response(status_code=status.HTTP_200_OK)


@router.post("/{device_id}/update")
def update_post(
    request: Request,
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    name: str = Form(...),
    device_type: str = Form(...),
    img_url: str | None = Form(None),
    ws_url: str | None = Form(None),
    api_key: str | None = Form(None),
    notes: str | None = Form(None),
    brightness: int = Form(...),
    night_brightness: int = Form(...),
    default_interval: int = Form(...),
    night_mode_enabled: bool = Form(False),
    night_start: int = Form(...),
    night_end: int = Form(...),
    night_mode_app: str | None = Form(None),
    timezone: str = Form(...),
    location: str | None = Form(None),
) -> Response:
    """Handle device update."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    error = None
    if not name or not device_id:
        error = "Id and Name is required."
    if error is not None:
        flash(request, error)
        return RedirectResponse(
            url=f"/{device_id}/update", status_code=status.HTTP_302_FOUND
        )

    device.name = name
    device.type = device_type
    device.img_url = (
        db.sanitize_url(img_url) if img_url else f"{server_root()}/{device_id}/next"
    )
    device.ws_url = (
        db.sanitize_url(ws_url) if ws_url else ws_root() + f"/{device_id}/ws"
    )
    device.api_key = api_key or ""
    device.notes = notes or ""
    device.brightness = db.ui_scale_to_percent(brightness)
    device.night_brightness = db.ui_scale_to_percent(night_brightness)
    device.default_interval = default_interval
    device.night_mode_enabled = night_mode_enabled
    device.night_start = night_start
    device.night_end = night_end
    device.night_mode_app = night_mode_app or ""
    device.timezone = timezone

    if location and location != "{}":
        try:
            loc = json.loads(location)
            lat = loc.get("lat")
            lng = loc.get("lng")
            if lat and lng:
                device.location = Location(
                    lat=lat,
                    lng=lng,
                    name=loc.get("name", ""),
                    timezone=loc.get("timezone", ""),
                )
            else:
                flash(request, "Invalid location")
        except json.JSONDecodeError as e:
            flash(request, f"Location JSON error {e}")

    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/delete")
def delete(
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle device deletion."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    user.devices.pop(device_id)
    db.save_user(db_conn, user)
    db.delete_device_dirs(device_id)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/addapp")
def addapp(
    request: Request,
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Render the add app page."""
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

    return templates.TemplateResponse(
        request,
        "manager/addapp.html",
        {
            "device": device,
            "apps_list": apps_list,
            "custom_apps_list": custom_apps_list,
            "user": user,
            "settings": settings,
        },
    )


@router.post("/{device_id}/addapp")
def addapp_post(
    request: Request,
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    name: str = Form(...),
    uinterval: int | None = Form(None),
    display_time: int | None = Form(None),
    notes: str | None = Form(None),
) -> Response:
    """Handle adding an app to a device."""
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


@router.get("/{device_id}/uploadapp")
def uploadapp(
    request: Request, device_id: str, user: User = Depends(manager)
) -> Response:
    """Render the upload app page."""
    user_apps_path = db.get_users_dir() / user.username / "apps"
    user_apps_path.mkdir(parents=True, exist_ok=True)
    star_files = [file.name for file in user_apps_path.rglob("*.star")]
    return templates.TemplateResponse(
        request,
        "manager/uploadapp.html",
        {"files": star_files, "device_id": device_id, "user": user},
    )


@router.post("/{device_id}/uploadapp")
async def uploadapp_post(
    request: Request,
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    """Handle app upload."""
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

    if not await db.save_user_app(file, app_subdir):
        flash(request, "Save Failed")
        return RedirectResponse(
            url=f"/{device_id}/uploadapp", status_code=status.HTTP_302_FOUND
        )

    flash(request, "Upload Successful")
    preview = db.get_data_dir() / "apps" / f"{app_name}.webp"
    render_app(db_conn, app_subdir, {}, preview, Device(id="aaaaaaaa"), None, logger)

    return RedirectResponse(
        url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
    )


@router.get("/app_preview/{filename}")
def app_preview(filename: str) -> FileResponse:
    """Serve app preview images."""
    file_path = db.get_data_dir() / "apps" / filename
    if not file_path.is_file():
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    return FileResponse(file_path)


@router.get("/{device_id}/deleteupload/{filename}")
def deleteupload(
    request: Request,
    device_id: str,
    filename: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle deletion of an uploaded app."""
    if any(
        Path(app.path).name == filename
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


@router.get("/{device_id}/{iname}/delete")
@router.post("/{device_id}/{iname}/delete")
def deleteapp(
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle deletion of an app from a device."""
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


@router.get("/{device_id}/{iname}/toggle_pin")
def toggle_pin(
    request: Request,
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Toggle pin/unpin for an app on a device."""
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


@router.get("/{device_id}/{iname}/updateapp")
def updateapp(
    request: Request, device_id: str, iname: str, user: User = Depends(manager)
) -> Response:
    """Render the app update page."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    # Set default dates if not already set
    today = date.today()
    if not app.recurrence_start_date:
        app.recurrence_start_date = today.strftime("%Y-%m-%d")
    if not app.recurrence_end_date:
        app.recurrence_end_date = (today + timedelta(days=7)).strftime("%Y-%m-%d")

    return templates.TemplateResponse(
        request,
        "manager/updateapp.html",
        {
            "app": app,
            "device_id": device_id,
            "config": json.dumps(app.config, indent=4),
            "user": user,
        },
    )


@router.post("/{device_id}/{iname}/updateapp")
def updateapp_post(
    request: Request,
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    name: str = Form(...),
    uinterval: int | None = Form(None),
    display_time: int = Form(0),
    notes: str | None = Form(None),
    enabled: bool = Form(False),
    start_time: str | None = Form(None),
    end_time: str | None = Form(None),
    days: list[str] = Form([]),
    use_custom_recurrence: bool = Form(False),
    recurrence_type: str | None = Form(None),
    recurrence_interval: int | None = Form(None),
    recurrence_start_date: str | None = Form(None),
    recurrence_end_date: str | None = Form(None),
    weekdays: list[str] = Form([]),
    monthly_pattern: str | None = Form(None),
    day_of_month: int | None = Form(None),
    day_of_week_pattern: str | None = Form(None),
) -> Response:
    """Handle app update."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    temp_app = app.model_copy(deep=True)
    temp_app.name = name
    temp_app.uinterval = uinterval or 0
    temp_app.display_time = display_time
    temp_app.notes = notes or ""
    temp_app.enabled = enabled
    temp_app.start_time = start_time
    temp_app.end_time = end_time
    temp_app.days = days
    temp_app.use_custom_recurrence = use_custom_recurrence
    temp_app.recurrence_type = recurrence_type or "daily"
    temp_app.recurrence_interval = recurrence_interval or 1
    temp_app.recurrence_start_date = recurrence_start_date or ""
    temp_app.recurrence_end_date = recurrence_end_date

    recurrence_pattern = RecurrencePattern()
    if use_custom_recurrence:
        if recurrence_type == "weekly":
            recurrence_pattern.weekdays = weekdays
        elif recurrence_type == "monthly":
            if monthly_pattern == "day_of_month":
                recurrence_pattern.day_of_month = day_of_month
            elif monthly_pattern == "day_of_week":
                recurrence_pattern.day_of_week = day_of_week_pattern
    temp_app.recurrence_pattern = recurrence_pattern

    if not name:
        flash(request, "Name is required.")
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

    app.name = name
    app.uinterval = uinterval or 0
    app.display_time = display_time
    app.notes = notes or ""
    app.enabled = enabled
    app.start_time = start_time
    app.end_time = end_time
    app.days = days
    app.use_custom_recurrence = use_custom_recurrence
    app.recurrence_type = recurrence_type or "daily"
    app.recurrence_interval = recurrence_interval or 1
    app.recurrence_start_date = recurrence_start_date or ""
    app.recurrence_end_date = recurrence_end_date
    app.recurrence_pattern = recurrence_pattern
    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/toggle_enabled")
def toggle_enabled(
    request: Request,
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Toggle the enabled state of an app."""
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


@router.get("/{device_id}/{iname}/moveapp")
def moveapp(
    request: Request,
    device_id: str,
    iname: str,
    direction: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Move an app up or down in the order."""
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


@router.get("/{device_id}/{iname}/configapp")
def configapp(
    request: Request,
    device_id: str,
    iname: str,
    delete_on_cancel: bool = False,
    user: User = Depends(manager),
) -> Response:
    """Render the app configuration page."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
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


@router.post("/{device_id}/{iname}/configapp")
async def configapp_post(
    request: Request,
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: Any = Body(...),
) -> Response:
    """Handle app configuration."""
    device = user.devices.get(device_id)
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
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


@router.get("/{device_id}/{iname}/preview")
def preview(
    request: Request,
    device_id: str,
    iname: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: str = "{}",
) -> Response:
    """Generate a preview of an app."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")

    app = device.apps.get(iname)
    if not app:
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
            raise HTTPException(status_code=500, detail="Error running pixlet render")

        return Response(content=data, media_type="image/webp")
    except Exception as e:
        logger.error(f"Error in preview: {e}")
        raise HTTPException(status_code=500, detail="Error generating preview")


@router.get("/adminindex")
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


@router.post("/admin/{username}/deleteuser")
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


@router.get("/{device_id}/firmware")
def generate_firmware(
    request: Request, device_id: str, user: User = Depends(manager)
) -> Response:
    """Render the firmware generation page."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")

    firmware_version = db.get_firmware_version()
    return templates.TemplateResponse(
        request,
        "manager/firmware.html",
        {"device": device, "firmware_version": firmware_version, "user": user},
    )


@router.post("/{device_id}/firmware")
def generate_firmware_post(
    device_id: str,
    user: User = Depends(manager),
    wifi_ap: str = Form(...),
    wifi_password: str = Form(...),
    img_url: str = Form(...),
    swap_colors: bool = Form(False),
) -> Response:
    """Handle firmware generation."""
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
        raise HTTPException(status_code=500, detail="Firmware generation failed")

    return Response(
        content=firmware_data,
        media_type="application/octet-stream",
        headers={
            "Content-Disposition": f"attachment;filename=firmware_{device.type}_{device_id}.bin"
        },
    )


@router.post("/set_user_repo")
def set_user_repo(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    app_repo_url: str = Form(...),
) -> Response:
    """Set the user's custom app repository."""
    apps_path = db.get_users_dir() / user.username / "apps"
    if set_repo(db_conn, request, user, "app_repo_url", apps_path, app_repo_url):
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.post("/set_api_key")
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


@router.post("/set_system_repo")
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


@router.post("/refresh_system_repo")
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


@router.post("/mark_app_broken/{app_name}")
def mark_app_broken(app_name: str, user: User = Depends(manager)) -> Response:
    """Mark an app as broken by adding it to broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if settings.PRODUCTION != "0":
        return JSONResponse(
            content={"success": False, "message": "Only available in development mode"},
            status_code=403,
        )

    # Only allow admin users
    if user.username != "admin":
        return JSONResponse(
            content={"success": False, "message": "Admin access required"},
            status_code=403,
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
                status_code=400,
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
            status_code=200,
        )

    except Exception as e:
        logger.error(f"Error marking app as broken: {e}")
        return JSONResponse(
            content={"success": False, "message": str(e)}, status_code=500
        )


@router.post("/unmark_app_broken/{app_name}")
def unmark_app_broken(app_name: str, user: User = Depends(manager)) -> Response:
    """Remove an app from broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if settings.PRODUCTION != "0":
        return JSONResponse(
            content={"success": False, "message": "Only available in development mode"},
            status_code=403,
        )

    # Only allow admin users
    if user.username != "admin":
        return JSONResponse(
            content={"success": False, "message": "Admin access required"},
            status_code=403,
        )

    try:
        # Get the broken_apps.txt path
        broken_apps_path = db.get_data_dir() / "system-apps" / "broken_apps.txt"

        # Read existing broken apps
        if not broken_apps_path.exists():
            return JSONResponse(
                content={"success": False, "message": "No broken apps file found"},
                status_code=404,
            )

        broken_apps = broken_apps_path.read_text().splitlines()

        # Add .star extension if not present
        app_filename = app_name if app_name.endswith(".star") else f"{app_name}.star"

        # Check if in the list
        if app_filename not in broken_apps:
            return JSONResponse(
                content={"success": False, "message": "App is not marked as broken"},
                status_code=400,
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
            status_code=200,
        )

    except Exception as e:
        logger.error(f"Error unmarking app: {e}")
        return JSONResponse(
            content={"success": False, "message": str(e)}, status_code=500
        )


@router.post("/update_firmware")
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


@router.post("/refresh_user_repo")
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


@router.get("/export_user_config")
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


@router.get("/{device_id}/export_config")
def export_device_config(device_id: str, user: User = Depends(manager)) -> Response:
    """Export device configuration as a JSON file."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=404, detail="Device not found")
    device_json = json.dumps(device.model_dump(), indent=4)
    return Response(
        content=device_json,
        media_type="application/json",
        headers={"Content-Disposition": f"attachment;filename={device_id}_config.json"},
    )


@router.get("/{device_id}/import_config")
def import_device_config(
    request: Request, device_id: str, user: User = Depends(manager)
) -> Response:
    """Render the import device configuration page."""
    return templates.TemplateResponse(
        request,
        "manager/import_config.html",
        {"device_id": device_id, "user": user},
    )


@router.post("/{device_id}/import_config")
async def import_device_config_post(
    request: Request,
    device_id: str,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    """Handle import of device configuration."""
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


@router.post("/import_user_config")
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


@router.get("/import_device")
def import_device(request: Request, user: User = Depends(manager)) -> Response:
    """Render the import device page."""
    return templates.TemplateResponse(
        request, "manager/import_config.html", {"user": user}
    )


@router.post("/import_device")
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


@router.get("/{device_id}/next")
def next_app(device_id: str, db_conn: sqlite3.Connection = Depends(get_db)) -> Response:
    """Get the next app for a device."""
    return _next_app_logic(db_conn, device_id)


@router.get("/{device_id}/brightness")
def get_brightness(
    device_id: str, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    """Get the brightness for a device."""
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    brightness_value = db.get_device_brightness_8bit(device)
    return Response(content=str(brightness_value), media_type="text/plain")


@router.get("/{device_id}/currentapp")
def currentwebp(
    device_id: str, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    """Get the current app's image for a device."""
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


@router.get("/{device_id}/{iname}/appwebp")
def appwebp(
    device_id: str, iname: str, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    """Get a specific app's image for a device."""
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


@router.post("/{device_id}/{iname}/schema_handler/{handler}")
async def schema_handler(
    request: Request,
    device_id: str,
    iname: str,
    handler: str,
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle schema-defined callbacks."""
    user = db.get_user_by_device_id(db_conn, device_id)
    if not user:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)
    app = device.apps.get(iname)
    if not app:
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


@router.get("/health")
def health() -> Response:
    """Health check endpoint."""
    return Response(content="OK", status_code=status.HTTP_200_OK)
