"""Manager router."""

import json
import logging
import secrets
import sqlite3
import string
import time
import uuid
from datetime import date, timedelta, datetime, timezone
from pathlib import Path
from random import randint
from typing import Annotated, Any, cast
from zoneinfo import available_timezones

from fastapi import (
    APIRouter,
    Body,
    Depends,
    File,
    Form,
    HTTPException,
    Query,
    Request,
    UploadFile,
    status,
)
from fastapi.responses import FileResponse, JSONResponse, RedirectResponse, Response
from fastapi_babel import _
from markupsafe import escape
from pydantic import BaseModel, BeforeValidator, ValidationError
from werkzeug.utils import secure_filename

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import Settings, get_settings
from tronbyt_server.dependencies import (
    DeviceAndApp,
    get_db,
    get_device_and_app,
    get_user_and_device,
    manager,
    UserAndDevice,
)
from tronbyt_server.flash import flash
from tronbyt_server.models import (
    DEFAULT_DEVICE_TYPE,
    App,
    Device,
    DeviceID,
    DeviceType,
    Location,
    RecurrencePattern,
    RecurrenceType,
    User,
    Weekday,
    ProtocolType,
    Brightness,
)
from tronbyt_server.pixlet import call_handler_with_config, get_schema
from tronbyt_server.templates import templates
from tronbyt_server.utils import (
    possibly_render,
    push_image,
    render_app,
    send_default_image,
    send_image,
    set_repo,
)

router = APIRouter(tags=["manager"])
logger = logging.getLogger(__name__)


def create_expanded_apps_list(device: Device, apps_list: list[App]) -> list[App]:
    """
    Create an expanded apps list with interstitial apps inserted between regular apps.

    Args:
        device: The device containing interstitial app settings
        apps_list: List of regular apps sorted by order

    Returns:
        List of apps with interstitial apps inserted after each regular app (except the last)
    """
    expanded_apps_list: list[App] = []
    interstitial_app_iname = device.interstitial_app
    interstitial_enabled = device.interstitial_enabled

    for i, regular_app in enumerate(apps_list):
        # Add the regular app
        expanded_apps_list.append(regular_app)

        # Add interstitial app after each regular app (except the last one)
        if (
            interstitial_enabled
            and interstitial_app_iname
            and interstitial_app_iname in device.apps
            and i < len(apps_list) - 1
        ):
            interstitial_app = device.apps[interstitial_app_iname]
            expanded_apps_list.append(interstitial_app)

    return expanded_apps_list


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


def _get_app_to_display(
    device: Device,
    last_app_index: int | None = None,
    advance_index: bool = False,
) -> tuple[App | None, int | None, bool, bool, bool]:
    """
    Determine which app to display based on current state.

    Returns:
        tuple: (app, new_index, is_pinned_app, is_night_mode_app, is_interstitial_app)
        If no app should be displayed, returns (None, None, False, False, False)
    """
    # if no apps return None
    if not device.apps:
        return None, None, False, False, False

    pinned_app_iname = device.pinned_app
    is_pinned_app = False
    is_night_mode_app = False
    is_interstitial_app = False

    if pinned_app_iname and pinned_app_iname in device.apps:
        logger.debug(f"Using pinned app: {pinned_app_iname}")
        app = device.apps[pinned_app_iname]
        is_pinned_app = True
        # For pinned apps, we don't update last_app_index since we're not cycling
        return (
            app,
            last_app_index,
            is_pinned_app,
            is_night_mode_app,
            is_interstitial_app,
        )
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
            return (
                app,
                last_app_index,
                is_pinned_app,
                is_night_mode_app,
                is_interstitial_app,
            )
        else:
            # Create expanded apps list with interstitial apps inserted
            expanded_apps_list = create_expanded_apps_list(device, apps_list)

            if last_app_index is None:
                last_app_index = device.last_app_index

            # Handle compatibility: if the index is too large for the expanded list,
            # it might be from the old system (before interstitial apps)
            if last_app_index >= len(expanded_apps_list):
                # If interstitial is enabled, the old index might be valid for the original apps_list
                if device.interstitial_enabled and last_app_index < len(apps_list):
                    # Use the original apps_list for backward compatibility
                    app = apps_list[last_app_index]
                else:
                    # Reset to 0 if index is completely invalid
                    app = expanded_apps_list[0]
                    last_app_index = 0
            else:
                app = expanded_apps_list[last_app_index]

            # Check if this is at an interstitial position
            # Interstitial positions are at odd indices (1, 3, 5, etc.)
            if (
                device.interstitial_enabled
                and device.interstitial_app
                and device.interstitial_app in device.apps
                and app.iname == device.interstitial_app
            ):
                # Check if we're at an odd index (interstitial position)
                is_interstitial_app = last_app_index % 2 == 1

            # Calculate new index if advancing
            new_index = last_app_index
            if advance_index:
                if last_app_index + 1 < len(expanded_apps_list):
                    new_index = last_app_index + 1
                else:
                    new_index = 0  # Reset to beginning of list

            return app, new_index, is_pinned_app, is_night_mode_app, is_interstitial_app


def next_app_logic(
    db_conn: sqlite3.Connection,
    user: User,
    device: Device,
    last_app_index: int | None = None,
    recursion_depth: int = 0,
) -> Response:
    device_id = device.id
    # first check for a pushed file starting with __ and just return that and then delete it.
    # This needs to happen before brightness check to clear any ephemeral images
    pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            logger.debug(f"Found pushed image {ephemeral_file}")
            response = send_image(ephemeral_file, device, None, True)
            ephemeral_file.unlink()
            return response

    if not device.apps:
        return send_default_image(device)

    # If brightness is 0, short-circuit and return default image to save processing
    brightness = device.brightness or Brightness(50)
    if brightness.as_percent == 0:
        logger.debug("Brightness is 0, returning default image")
        return send_default_image(device)

    # For /next endpoint: advance the index FIRST, then get the app to display
    # This ensures we return the NEXT app, not the current one
    if last_app_index is None:
        last_app_index = device.last_app_index

    # Get the apps list from the already-fetched device
    apps_list = sorted([app for app in device.apps.values()], key=lambda x: x.order)
    expanded_apps_list = create_expanded_apps_list(device, apps_list)

    if recursion_depth > len(expanded_apps_list):
        logger.warning("Maximum recursion depth exceeded, sending default image")
        return send_default_image(device)

    # Calculate the next index
    if last_app_index + 1 < len(expanded_apps_list):
        next_index = last_app_index + 1
    else:
        next_index = 0  # Reset to beginning of list

    # Get the app at the next index (without advancing further)
    app, _, is_pinned_app, is_night_mode_app, is_interstitial_app = _get_app_to_display(
        device, next_index, advance_index=False
    )

    if app is None:
        return send_default_image(device)

    # For pinned apps, always display them regardless of enabled/schedule status
    # For interstitial apps at interstitial positions, always display them
    # For interstitial apps at regular positions, check if enabled
    # For other apps, check if they should be displayed
    if (
        not is_pinned_app
        and not is_night_mode_app
        and not is_interstitial_app  # Skip enabled check if at interstitial position
        and (not app.enabled or not db.get_is_app_schedule_active(app, device))
    ):
        # Pass next_index directly since it already points to the next app
        return next_app_logic(db_conn, user, device, next_index, recursion_depth + 1)

    # If the app is the interstitial app but we're NOT at an interstitial position,
    # check if it's enabled for regular rotation
    if (
        app.iname == device.interstitial_app
        and device.interstitial_app in device.apps
        and not is_interstitial_app
        and not app.enabled
    ):
        # Interstitial app at regular position is disabled - skip it
        return next_app_logic(db_conn, user, device, next_index, recursion_depth + 1)

    # NEW: Skip interstitial app if the previous regular app was skipped
    # If we're at an interstitial position and the previous regular app (at index-1) would be skipped,
    # then we should skip this interstitial app too to avoid showing it in isolation
    if is_interstitial_app:
        # We're at an interstitial position (odd index)
        # Check if the previous regular app (at next_index - 1) would be skipped
        prev_index = next_index - 1
        if prev_index >= 0 and prev_index < len(expanded_apps_list):
            prev_app = expanded_apps_list[prev_index]
            # Check if the previous app would be skipped
            if prev_app.iname in device.apps:
                prev_app_obj = device.apps[prev_app.iname]
                # Check if previous app would be skipped (enabled, schedule, or empty render)
                # Note: We don't call possibly_render here as it's expensive and unnecessary
                # since we'll discover empty renders when we try to display the app anyway
                if (
                    not prev_app_obj.enabled
                    or not db.get_is_app_schedule_active(prev_app_obj, device)
                    or prev_app_obj.empty_last_render
                ):
                    # Previous app would be skipped - skip this interstitial too
                    return next_app_logic(
                        db_conn, user, device, next_index, recursion_depth + 1
                    )

    if not possibly_render(db_conn, user, device_id, app) or app.empty_last_render:
        # App failed to render or had empty render - skip it.
        # If this is a pinned app we should unpin it because it's not Active
        # pinned app is set as the app iname in the device object, we need to clear it.
        if getattr(device, "pinned_app", None) == app.iname:
            logger.info(f"Unpinning app {app.iname} because fail or empty render")
            try:
                with db.db_transaction(db_conn) as cursor:
                    db.update_device_field(
                        cursor, user.username, device.id, "pinned_app", ""
                    )
                # Keep the in-memory object in sync
                device.pinned_app = ""
            except sqlite3.Error as e:
                logger.error(f"Failed to unpin app for device {device.id}: {e}")

        # Pass next_index directly since it already points to the next app
        return next_app_logic(db_conn, user, device, next_index, recursion_depth + 1)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device_id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device_id) / f"{app.name}-{app.iname}.webp"

    if webp_path.exists() and webp_path.stat().st_size > 0:
        # App rendered successfully - display it and save the index
        response = send_image(webp_path, device, app)
        # Atomically save the next_index as the new last_app_index
        try:
            with db.db_transaction(db_conn) as cursor:
                db.update_device_field(
                    cursor, user.username, device.id, "last_app_index", next_index
                )
            # Keep the in-memory object in sync for the current request
            device.last_app_index = next_index
        except sqlite3.Error as e:
            logger.error(f"Failed to update last_app_index for device {device.id}: {e}")
        return response

    # WebP file doesn't exist or is empty - skip this app
    # Pass next_index directly since it already points to the next app
    return next_app_logic(db_conn, user, device, next_index, recursion_depth + 1)


@router.get("/")
def index(
    request: Request,
    user: User = Depends(manager),
) -> Response:
    """Render the main page with a list of devices."""
    devices = reversed(list(user.devices.values()))
    return templates.TemplateResponse(
        request, "manager/index.html", {"devices": devices, "user": user}
    )


@router.get("/create")
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


@router.post("/create")
def create_post(
    request: Request,
    form_data: Annotated[DeviceCreateFormData, Form()],
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle device creation."""
    error = None
    if not form_data.name or db.get_device_by_name(user, form_data.name):
        error = _("Unique name is required.")
    if error is not None:
        flash(request, error)
        return templates.TemplateResponse(
            request,
            "manager/create.html",
            {"user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    max_attempts = 10
    for _i in range(max_attempts):
        device_id = str(uuid.uuid4())[0:8]
        if device_id not in user.devices:
            break
    else:
        flash(request, _("Could not generate a unique device ID."))
        return templates.TemplateResponse(
            request,
            "manager/create.html",
            {"user": user},
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )

    img_url = form_data.img_url or str(request.url_for("next_app", device_id=device_id))
    ws_url = form_data.ws_url or str(
        request.url_for("websocket_endpoint", device_id=device_id)
    )
    api_key = form_data.api_key or "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )

    percent_brightness = Brightness.from_ui_scale(form_data.brightness)

    device = Device(
        id=device_id,
        name=form_data.name or device_id,
        type=cast(DeviceType, form_data.device_type),
        img_url=img_url,
        ws_url=ws_url,
        api_key=api_key,
        brightness=percent_brightness,
        night_brightness=Brightness(0),
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
                flash(request, _("Invalid location"))
        except json.JSONDecodeError as e:
            flash(request, _("Location JSON error {error}").format(error=e))

    user.devices[device.id] = device
    if db.save_user(db_conn, user) and not db.get_device_webp_dir(device.id).is_dir():
        db.get_device_webp_dir(device.id).mkdir(parents=True)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/update")
def update(
    request: Request,
    deps: UserAndDevice = Depends(get_user_and_device),
) -> Response:
    device = deps.device
    if not device:
        return RedirectResponse(url="/", status_code=status.HTTP_404_NOT_FOUND)

    default_img_url = request.url_for("next_app", device_id=device.id)
    default_ws_url = str(request.url_for("websocket_endpoint", device_id=device.id))

    return templates.TemplateResponse(
        request,
        "manager/update.html",
        {
            "device": device,
            "available_timezones": available_timezones(),
            "default_img_url": default_img_url,
            "default_ws_url": default_ws_url,
            "user": deps.user,
        },
    )


@router.post("/{device_id}/update_brightness")
async def update_brightness(
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

    device_brightness = Brightness.from_ui_scale(brightness)
    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_device_field(
                cursor,
                user.username,
                device.id,
                "brightness",
                device_brightness.as_percent,
            )
        device.brightness = device_brightness  # keep in-memory model updated
    except sqlite3.Error as e:
        logger.error(f"Failed to update brightness for device {device.id}: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Database error",
        )

    new_brightness_percent = db.get_device_brightness_percent(device)

    # Send brightness command directly to active websocket connection (if any)
    # This doesn't interrupt the natural flow of rotation
    from tronbyt_server.routers.websockets import send_brightness_update

    await send_brightness_update(device_id, new_brightness_percent)

    logger.info(
        f"[{device_id}] Queued brightness update for delivery: {new_brightness_percent}"
    )
    return Response(status_code=status.HTTP_200_OK)


@router.post("/{device_id}/update_interval")
def update_interval(
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    interval: int = Form(...),
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_device_field(
                cursor, user.username, device.id, "default_interval", interval
            )
        device.default_interval = interval
    except sqlite3.Error as e:
        logger.error(f"Failed to update interval for device {device.id}: {e}")
        return Response(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            content="Database error",
        )
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
    interstitial_enabled: bool = False
    interstitial_app: str | None = None


@router.post("/{device_id}/update")
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
        error = _("Id and Name is required.")
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
        else str(request.url_for("next_app", device_id=device_id))
    )
    device.ws_url = (
        db.sanitize_url(form_data.ws_url)
        if form_data.ws_url
        else str(request.url_for("websocket_endpoint", device_id=device_id))
    )
    device.api_key = form_data.api_key or ""
    device.notes = form_data.notes or ""
    device.brightness = Brightness.from_ui_scale(form_data.brightness)
    device.night_brightness = Brightness.from_ui_scale(form_data.night_brightness)
    device.default_interval = form_data.default_interval
    device.night_mode_enabled = form_data.night_mode_enabled
    device.night_mode_app = form_data.night_mode_app or ""
    device.timezone = form_data.timezone

    if form_data.night_start:
        try:
            device.night_start = parse_time_input(form_data.night_start)
        except ValueError as e:
            flash(request, _("Invalid night start time: {error}").format(error=e))

    if form_data.night_end:
        try:
            device.night_end = parse_time_input(form_data.night_end)
        except ValueError as e:
            flash(request, _("Invalid night end time: {error}").format(error=e))

    # Handle dim time and dim brightness
    # Note: Dim mode ends at night_end time (if set) or 6:00 AM by default
    if form_data.dim_time and form_data.dim_time.strip():
        try:
            device.dim_time = parse_time_input(form_data.dim_time)
        except ValueError as e:
            flash(request, _("Invalid dim time: {error}").format(error=e))
    elif device.dim_time:
        # Remove dim_time if the field is empty
        device.dim_time = None

    if form_data.dim_brightness is not None:
        device.dim_brightness = Brightness.from_ui_scale(form_data.dim_brightness)

    # Handle interstitial app settings
    device.interstitial_enabled = form_data.interstitial_enabled
    if form_data.interstitial_app and form_data.interstitial_app != "None":
        device.interstitial_app = form_data.interstitial_app
    else:
        device.interstitial_app = None

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
                flash(request, _("Invalid location"))
        except json.JSONDecodeError as e:
            flash(request, _("Location JSON error {error}").format(error=e))

    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/delete")
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


@router.get("/{device_id}/addapp")
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
        app_metadata.is_installed = app_metadata.name in installed_app_names

    # Also mark installed status for custom apps
    for app_metadata in custom_apps_list:
        app_metadata.is_installed = app_metadata.name in installed_app_names

    # Sort apps_list so that installed apps appear first
    apps_list.sort(key=lambda app_metadata: not app_metadata.is_installed)

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


@router.post("/{device_id}/addapp")
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
        flash(request, _("App name required."))
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    max_attempts = 10
    for _i in range(max_attempts):
        iname = str(randint(100, 999))
        if iname not in device.apps:
            break
    else:
        flash(request, _("Could not generate a unique installation ID."))
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    app_details = db.get_app_details_by_name(user.username, name)
    if not app_details:
        flash(request, _("App not found."))
        return RedirectResponse(
            url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
        )

    logger.info(
        f"Adding app {name}: uinterval from form={uinterval}, recommended_interval={app_details.recommended_interval}"
    )

    # Use recommended_interval if:
    # 1. uinterval is None (not provided), OR
    # 2. uinterval is 10 (the default) AND recommended_interval is different from 10
    #    (this includes 0, which should be respected)
    if uinterval is None:
        final_uinterval = app_details.recommended_interval
    elif uinterval == 10 and app_details.recommended_interval != 10:
        # User likely didn't change the default, use recommended interval instead
        # This works even if recommended_interval is 0
        final_uinterval = app_details.recommended_interval
    else:
        # User explicitly set a value, or recommended_interval is also 10
        final_uinterval = uinterval

    app = App(
        id=app_details.id or "",
        name=name,
        iname=iname,
        path=app_details.path,
        enabled=False,
        last_render=0,
        uinterval=final_uinterval,
        display_time=display_time if display_time is not None else 0,
        notes=notes or "",
        order=len(device.apps),
    )

    logger.info(f"Created app with uinterval={app.uinterval}")

    device.apps[iname] = app
    db.save_user(db_conn, user)

    return RedirectResponse(
        url=f"{request.url_for('configapp', device_id=device_id, iname=iname)}?delete_on_cancel=true",
        status_code=status.HTTP_302_FOUND,
    )


@router.get("/{device_id}/uploadapp")
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


@router.post("/{device_id}/uploadapp")
async def uploadapp_post(
    request: Request,
    device_id: DeviceID,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    user_apps_path = db.get_users_dir() / user.username / "apps"
    if not file.filename:
        flash(request, _("No file"))
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
        flash(request, _("Invalid file path"))
        return templates.TemplateResponse(
            request,
            "manager/uploadapp.html",
            {"device_id": device_id, "user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    if not await db.save_user_app(file, app_subdir):
        flash(request, _("File type not allowed"))
        return templates.TemplateResponse(
            request,
            "manager/uploadapp.html",
            {"device_id": device_id, "user": user},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    flash(request, _("Upload Successful"))
    preview = db.get_data_dir() / "apps" / f"{app_name}.webp"
    render_app(
        db_conn,
        app_subdir,
        {},
        preview,
        Device(
            id="aaaaaaaa",
            brightness=Brightness(100),
            night_brightness=Brightness(0),
            dim_brightness=None,
            default_interval=15,
        ),
        None,
        user,
    )

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
            _("Cannot delete {filename} because it is installed on a device.").format(
                filename=filename
            ),
        )
    else:
        db.delete_user_upload(user, filename)

    return RedirectResponse(
        url=f"/{device_id}/addapp", status_code=status.HTTP_302_FOUND
    )


@router.post("/{device_id}/{iname}/delete")
def deleteapp(
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = device_and_app.device
    app = device_and_app.app
    iname = app.iname

    if app.pushed:
        webp_path = db.get_device_webp_dir(device.id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device.id) / f"{app.name}-{app.iname}.webp"

    if webp_path.is_file():
        webp_path.unlink()

    device.apps.pop(iname)
    db.save_user(db_conn, user)
    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/{iname}/toggle_pin")
def toggle_pin(
    request: Request,
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = device_and_app.device
    iname = device_and_app.app.iname

    new_pinned_app = "" if getattr(device, "pinned_app", None) == iname else iname

    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_device_field(
                cursor, user.username, device.id, "pinned_app", new_pinned_app
            )
        device.pinned_app = new_pinned_app
        if new_pinned_app:
            flash(request, _("App pinned."))
        else:
            flash(request, _("App unpinned."))
    except sqlite3.Error as e:
        logger.error(f"Failed to toggle pin for device {device.id}: {e}")
        flash(request, _("Error updating pin status."), "error")

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/{iname}/duplicate")
def duplicate_app(
    request: Request,
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Duplicate an existing app on a device."""
    device = device_and_app.device
    device_id = device.id

    # Get the original app
    original_app = device_and_app.app

    # Generate a unique iname for the duplicate
    max_attempts = 10
    for _i in range(max_attempts):
        new_iname = str(randint(100, 999))
        if new_iname not in device.apps:
            break
    else:
        flash(request, _("Could not generate a unique installation ID."))
        return Response(
            "Error generating unique ID",
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )

    # Create a copy of the original app with the new iname
    duplicated_app = App(
        name=original_app.name,
        iname=new_iname,
        enabled=original_app.enabled,
        last_render=0,  # Reset render time for new app
        uinterval=original_app.uinterval,
        display_time=original_app.display_time,
        notes=original_app.notes,
        config=original_app.config.copy(),  # Deep copy the config
        path=original_app.path,
        id=original_app.id,
        empty_last_render=original_app.empty_last_render,
        render_messages=original_app.render_messages.copy()
        if original_app.render_messages
        else [],
        start_time=original_app.start_time,
        end_time=original_app.end_time,
        days=original_app.days.copy() if original_app.days else [],
        use_custom_recurrence=original_app.use_custom_recurrence,
        recurrence_type=original_app.recurrence_type,
        recurrence_interval=original_app.recurrence_interval,
        recurrence_pattern=original_app.recurrence_pattern,
        recurrence_start_date=original_app.recurrence_start_date,
        recurrence_end_date=original_app.recurrence_end_date,
        pushed=original_app.pushed,
        order=0,  # Will be set below
    )

    # Get all apps and sort by order to find the original app's position
    apps_list = list(device.apps.values())
    apps_list.sort(key=lambda x: x.order)

    # Find the original app's position
    original_order = original_app.order

    # Update order for all apps that come after the original app
    for app_item in apps_list:
        if app_item.order > original_order:
            app_item.order = app_item.order + 1

    # Set the duplicate app's order to be right after the original
    duplicated_app.order = original_order + 1

    # Add the duplicated app to the device
    device.apps[new_iname] = duplicated_app

    # Save the user data first
    db.save_user(db_conn, user)

    # Render the duplicated app to generate its preview
    try:
        possibly_render(db_conn, user, device_id, duplicated_app)
    except Exception as e:
        logger.error(f"Error rendering duplicated app {new_iname}: {e}")
        # Don't fail the duplication if rendering fails, just log it

    # Check if this is an AJAX request
    if request.headers.get("Content-Type") == "application/x-www-form-urlencoded":
        return Response("OK", status_code=status.HTTP_200_OK)
    else:
        flash(request, _("App duplicated successfully."))
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/updateapp")
def updateapp(
    request: Request,
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
) -> Response:
    device = device_and_app.device
    app = device_and_app.app
    device_id = device.id

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
            "form": {},  # Empty form data for GET request
        },
    )


def empty_str_to_none(v: Any) -> Any:
    """Convert empty strings to None."""
    if v == "":
        return None
    return v


class AppUpdateFormData(BaseModel):
    """Represents the form data for updating an app."""

    name: str
    uinterval: Annotated[int | None, BeforeValidator(empty_str_to_none)] = None
    display_time: int = 0
    notes: str | None = None
    enabled: bool = False
    autopin: bool = False
    start_time: str | None = None
    end_time: str | None = None
    days: list[str] = []
    use_custom_recurrence: bool = False
    recurrence_type: str | None = None
    recurrence_interval: Annotated[int | None, BeforeValidator(empty_str_to_none)] = (
        None
    )
    recurrence_start_date: str | None = None
    recurrence_end_date: str | None = None
    weekdays: list[str] = []
    monthly_pattern: str | None = None
    day_of_month: Annotated[int | None, BeforeValidator(empty_str_to_none)] = None
    day_of_week_pattern: str | None = None


@router.post("/{device_id}/{iname}/updateapp")
def updateapp_post(
    request: Request,
    form_data: Annotated[AppUpdateFormData, Form()],
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle app update."""
    device = device_and_app.device
    app = device_and_app.app

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
        "autopin": form_data.autopin,
        "start_time": form_data.start_time,
        "end_time": form_data.end_time,
        "days": form_data.days,
        "use_custom_recurrence": form_data.use_custom_recurrence,
        "recurrence_type": form_data.recurrence_type or RecurrenceType.DAILY,
        "recurrence_interval": form_data.recurrence_interval or 1,
        "recurrence_pattern": recurrence_pattern,
    }
    if form_data.recurrence_start_date:
        update_data["recurrence_start_date"] = form_data.recurrence_start_date
    if form_data.recurrence_end_date:
        update_data["recurrence_end_date"] = form_data.recurrence_end_date

    if not form_data.name:
        flash(request, _("Name is required."))
        temp_app = app.model_copy(update=update_data)
        return templates.TemplateResponse(
            request,
            "manager/updateapp.html",
            {
                "app": temp_app,
                "device_id": device.id,
                "config": json.dumps(temp_app.config, indent=4),
                "user": user,
            },
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    for key, value in update_data.items():
        setattr(app, key, value)

    db.save_user(db_conn, user)

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/{iname}/toggle_enabled")
def toggle_enabled(
    request: Request,
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    app = device_and_app.app

    new_enabled_status = not app.enabled
    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_app_field(
                cursor,
                user.username,
                device_and_app.device.id,
                app.iname,
                "enabled",
                new_enabled_status,
            )
        app.enabled = new_enabled_status
        flash(request, _("Changes saved."))
    except sqlite3.Error as e:
        logger.error(f"Failed to toggle enabled for app {app.iname}: {e}")
        flash(request, _("Error saving changes."), "error")

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.post("/{device_id}/{iname}/moveapp")
def moveapp(
    request: Request,
    direction: str = Query(...),
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    if direction not in ["up", "down", "top", "bottom"]:
        flash(request, _("Invalid direction."))
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    device = device_and_app.device
    iname = device_and_app.app.iname

    apps_list = sorted(device.apps.values(), key=lambda x: x.order)
    current_idx = -1
    for i, app in enumerate(apps_list):
        if app.iname == iname:
            current_idx = i
            break

    if current_idx == -1:
        flash(request, _("App not found."))
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    if direction == "up":
        if current_idx == 0:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        target_idx = current_idx - 1
        apps_list[current_idx], apps_list[target_idx] = (
            apps_list[target_idx],
            apps_list[current_idx],
        )
    elif direction == "down":
        if current_idx == len(apps_list) - 1:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        target_idx = current_idx + 1
        apps_list[current_idx], apps_list[target_idx] = (
            apps_list[target_idx],
            apps_list[current_idx],
        )
    elif direction == "top":
        if current_idx == 0:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        # Move app to the top (index 0)
        app_to_move = apps_list.pop(current_idx)
        apps_list.insert(0, app_to_move)
    elif direction == "bottom":
        if current_idx == len(apps_list) - 1:
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
        # Move app to the bottom (last index)
        app_to_move = apps_list.pop(current_idx)
        apps_list.append(app_to_move)

    for i, app in enumerate(apps_list):
        app.order = i

    db.save_user(db_conn, user)
    return Response("OK", status_code=status.HTTP_200_OK)


@router.get("/{device_id}/{iname}/configapp")
def configapp(
    request: Request,
    delete_on_cancel: bool = False,
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
) -> Response:
    device = device_and_app.device
    app = device_and_app.app
    if not app or not app.path:
        flash(request, _("Error saving app, please try again."))
        return RedirectResponse(
            url=f"/{device.id}/addapp", status_code=status.HTTP_302_FOUND
        )

    schema_json = get_schema(Path(app.path))
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
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: Any = Body(...),
) -> Response:
    device = device_and_app.device
    app = device_and_app.app
    device_id = device.id
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
        user,
    )
    if image is not None:
        app.enabled = True
        app.last_render = int(time.time())
        db.save_app(db_conn, device_id, app)
    else:
        flash(request, _("Error Rendering App"))

    return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/{iname}/preview")
def preview(
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    config: str = "{}",
) -> Response:
    device = device_and_app.device
    app = device_and_app.app

    if not app or not app.path:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App not found"
        )

    try:
        config_data = json.loads(config)
    except json.JSONDecodeError:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST, detail="Invalid config JSON"
        )

    app_path = Path(app.path)
    if not app_path.exists():
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App path not found"
        )

    try:
        data = render_app(
            db_conn=db_conn,
            app_path=app_path,
            config=config_data,
            webp_path=None,
            device=device,
            app=app,
            user=user,
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


@router.post("/{device_id}/{iname}/preview")
async def push_preview(
    device_and_app: DeviceAndApp = Depends(get_device_and_app),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    device = device_and_app.device
    app = device_and_app.app

    if not app.path:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App not found"
        )

    app_path = Path(app.path)
    if not app_path.exists():
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App path not found"
        )

    try:
        # Render the app to get the image bytes directly
        image_bytes = render_app(
            db_conn=db_conn,
            app_path=app_path,
            config=app.config,
            webp_path=None,
            device=device,
            app=app,
            user=user,
        )
        if image_bytes is None:
            raise HTTPException(
                status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
                detail="Error running pixlet render",
            )

        # Use push_image to save the temporary preview and notify the device
        await push_image(device.id, None, image_bytes)

        return Response(status_code=status.HTTP_200_OK)
    except HTTPException:
        raise
    except Exception as e:
        logger.error(f"Error in push_preview: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Error generating preview",
        )


@router.get("/adminindex")
def adminindex(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Render the admin index page."""
    if user.username != "admin":
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
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
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
    if username != "admin":
        db.delete_user(db_conn, username)
    return RedirectResponse(url="/adminindex", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/firmware")
def generate_firmware(
    request: Request, device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )

    firmware_version = db.get_firmware_version()
    return templates.TemplateResponse(
        request,
        "manager/firmware.html",
        {"device": device, "firmware_version": firmware_version, "user": user},
    )


@router.post("/{device_id}/firmware")
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
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )

    try:
        firmware_data = firmware_utils.generate_firmware(
            url=img_url,
            ap=wifi_ap,
            pw=wifi_password,
            device_type=device.type,
            swap_colors=swap_colors,
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
            "Content-Disposition": f"attachment;filename=firmware_{device.type.value}_{device_id}.bin"
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

    # Ensure apps_path is within the expected users directory to prevent path traversal
    try:
        apps_path.relative_to(db.get_users_dir())
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in apps_path")
        return Response(
            status_code=status.HTTP_400_BAD_REQUEST, content="Invalid repository path"
        )

    if set_repo(request, apps_path, user.app_repo_url, app_repo_url):
        user.app_repo_url = app_repo_url
        db.save_user(db_conn, user)
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
        flash(request, _("API Key cannot be empty."))
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
    settings: Settings = Depends(get_settings),
) -> Response:
    """Set the system app repository (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
    logger.info(f"Setting system app repo to {app_repo_url}")
    if not app_repo_url:
        app_repo_url = settings.SYSTEM_APPS_REPO
    if set_repo(
        request,
        db.get_data_dir() / "system-apps",
        user.system_repo_url,
        app_repo_url,
    ):
        user.system_repo_url = app_repo_url
        db.save_user(db_conn, user)
        system_apps.generate_apps_json(db.get_data_dir())
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.post("/refresh_system_repo")
def refresh_system_repo(
    request: Request,
    user: User = Depends(manager),
) -> Response:
    """Refresh the system app repository (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
    # Directly update the system repo - it handles git pull internally
    system_apps.update_system_repo(db.get_data_dir())
    flash(request, _("System repo updated successfully"))
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.post("/mark_app_broken/{app_name}")
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
        system_apps.generate_apps_json(db.get_data_dir())

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


@router.post("/unmark_app_broken/{app_name}")
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
        system_apps.generate_apps_json(db.get_data_dir())

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


@router.post("/update_firmware")
def update_firmware(request: Request, user: User = Depends(manager)) -> Response:
    """Update firmware binaries (admin only)."""
    if user.username != "admin":
        raise HTTPException(status_code=status.HTTP_403_FORBIDDEN, detail="Forbidden")
    try:
        result = firmware_utils.update_firmware_binaries(db.get_data_dir())
        if result["success"]:
            if result["action"] == "updated":
                flash(request, _("✅ {result['message']}"), "success")
            elif result["action"] == "skipped":
                flash(request, _("ℹ️ {result['message']}"), "info")
        else:
            flash(request, _("❌ {result['message']}"), "error")
    except Exception as e:
        logger.error(f"Error updating firmware: {e}")
        flash(request, _("❌ Firmware update failed: {str(e)}"), "error")
    return RedirectResponse(
        url="/auth/edit#firmware-management",
        status_code=status.HTTP_302_FOUND,
    )


@router.post("/refresh_user_repo")
def refresh_user_repo(
    request: Request,
    user: User = Depends(manager),
) -> Response:
    """Refresh the user's custom app repository."""
    apps_path = db.get_users_dir() / user.username / "apps"
    if set_repo(request, apps_path, user.app_repo_url, user.app_repo_url):
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.get("/export_user_config")
def export_user_config(user: User = Depends(manager)) -> Response:
    """Export user configuration as a JSON file."""
    user_dict = user.model_dump(mode="json")
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
def export_device_config(
    device_id: DeviceID, user: User = Depends(manager)
) -> Response:
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )
    device_json = json.dumps(device.model_dump(mode="json"), indent=4)

    # Create filename with device name if available
    device_name = device.name if device.name else device_id
    # Sanitize the device name for use in filename (remove/replace invalid characters)
    safe_name = "".join(
        c if c.isalnum() or c in (" ", "-", "_") else "_" for c in device_name
    )
    safe_name = safe_name.replace(" ", "_")
    filename = f"{safe_name}_config.json"

    return Response(
        content=device_json,
        media_type="application/json",
        headers={"Content-Disposition": f"attachment;filename={filename}"},
    )


@router.get("/{device_id}/import_config")
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
        flash(request, _("No selected file"))
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )
    if not file.filename.endswith(".json"):
        flash(request, _("Invalid file type. Please upload a JSON file."))
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )

    try:
        contents = await file.read()
        device_config = json.loads(contents)
        if not isinstance(device_config, dict):
            flash(request, _("Invalid JSON structure"))
            return RedirectResponse(
                url=f"/{device_id}/import_config",
                status_code=status.HTTP_302_FOUND,
            )
        if "id" not in device_config:
            flash(request, _("Invalid config file: missing device ID"))
            return RedirectResponse(
                url=f"/{device_id}/import_config",
                status_code=status.HTTP_302_FOUND,
            )
        if device_config["id"] != device_id:
            flash(request, _("Not the same device id. Import skipped."))
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

        # Regenerate URLs with current server root
        try:
            device = Device.model_validate(cast(dict[str, Any], device_config))
        except ValidationError as e:
            logger.error(
                "Imported device validation for device '%s' failed: %s", device_id, e
            )
            flash(request, _("Invalid device configuration file"))
            return RedirectResponse(
                url=f"/{device_id}/import_config", status_code=status.HTTP_302_FOUND
            )
        device.img_url = str(request.url_for("next_app", device_id=device_id))
        device.ws_url = str(request.url_for("websocket_endpoint", device_id=device_id))
        user.devices[device.id] = device
        db.save_user(db_conn, user)
        flash(request, _("Device configuration imported successfully"))
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        flash(request, _("Error parsing JSON file: {error}").format(error=e))
        return RedirectResponse(
            url=f"/{device_id}/import_config",
            status_code=status.HTTP_302_FOUND,
        )
    except Exception as e:
        flash(request, _("Error importing config: {error}").format(error=str(e)))
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
        flash(request, _("No selected file"))
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    if not file.filename.endswith(".json"):
        flash(request, _("Invalid file type. Please upload a JSON file."))
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)

    try:
        contents = await file.read()
        user_config_raw = json.loads(contents)
        if not isinstance(user_config_raw, dict):
            flash(request, _("Invalid JSON structure"))
            return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
        user_config = cast(dict[str, Any], user_config_raw)

        # Replace all user data except username and password
        current_username = user.username
        current_password = user.password

        user_config["username"] = current_username
        user_config["password"] = current_password

        # Regenerate img_url and ws_url with new server root for all devices
        if "devices" in user_config:
            for device_id, device_data in user_config["devices"].items():
                device_data["img_url"] = str(
                    request.url_for("next_app", device_id=device_id)
                )
                device_data["ws_url"] = str(
                    request.url_for("websocket_endpoint", device_id=device_id)
                )

        logging.info(f"Attempting to import user config: {user_config.keys()}")
        try:
            new_user = User.model_validate(user_config)
            logging.info(
                f"Successfully created User object with {len(new_user.devices)} devices"
            )
        except ValidationError as validation_error:
            logging.error(
                f"Validation error during import for user '{current_username}': {validation_error}"
            )
            # Let outer except catch and flash a message
            raise

        db.save_user(db_conn, new_user)
        flash(request, _("User configuration imported successfully"))
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        logging.error(f"JSON decode error during user config import: {e}")
        flash(request, _("Error parsing JSON file: {error}").format(error=e))
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)
    except Exception as e:
        logging.error(f"Error importing user config: {e}", exc_info=True)
        flash(request, _("Error importing config: {error}").format(error=str(e)))
        return RedirectResponse(url="/auth/edit", status_code=status.HTTP_302_FOUND)


@router.get("/import_device", name="import_device")
def import_device(request: Request, user: User = Depends(manager)) -> Response:
    """Render the import device page."""
    return templates.TemplateResponse(
        request, "manager/import_config.html", {"user": user}
    )


@router.post("/import_device", name="import_device_post")
async def import_device_post(
    request: Request,
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
    file: UploadFile = File(...),
) -> Response:
    """Handle import of a new device."""
    if not file.filename:
        flash(request, _("No selected file"))
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)
    if not file.filename.endswith(".json"):
        flash(request, _("Invalid file type. Please upload a JSON file."))
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)

    try:
        contents = await file.read()
        device_config_raw = json.loads(contents)
        if not isinstance(device_config_raw, dict):
            flash(request, _("Invalid JSON structure"))
            return RedirectResponse(
                url="/import_device", status_code=status.HTTP_302_FOUND
            )

        device_config = cast(dict[str, Any], device_config_raw)
        device_id = str(device_config.get("id", ""))
        if not device_id:
            flash(request, _("Device ID missing in config."))
            return RedirectResponse(
                url="/import_device", status_code=status.HTTP_302_FOUND
            )
        if device_id in user.devices or db.get_device_by_id(db_conn, device_id):
            flash(request, _("Device already exists. Import skipped."))
            return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

        # Regenerate URLs with current server root
        try:
            device = Device.model_validate(device_config)
        except ValidationError as e:
            logger.error(
                "Imported device validation for device '%s' failed: %s", device_id, e
            )
            flash(request, _("Invalid device configuration file"))
            return RedirectResponse(
                url="/import_device", status_code=status.HTTP_302_FOUND
            )
        device.img_url = str(request.url_for("next_app", device_id=device_id))
        device.ws_url = str(request.url_for("websocket_endpoint", device_id=device_id))
        user.devices[device.id] = device
        db.save_user(db_conn, user)
        flash(request, _("Device configuration imported successfully"))
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    except json.JSONDecodeError as e:
        flash(request, _("Error parsing JSON file: {error}").format(error=e))
        return RedirectResponse(url="/import_device", status_code=status.HTTP_302_FOUND)


@router.get("/{device_id}/next", name="next_app")
def next_app(
    deps: UserAndDevice = Depends(get_user_and_device),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    user = deps.user
    device = deps.device

    now = datetime.now(timezone.utc)
    try:
        with db.db_transaction(db_conn) as cursor:
            db.update_device_field(
                cursor,
                user.username,
                device.id,
                "last_seen",
                now.isoformat(),
            )
            db.update_device_field(
                cursor,
                user.username,
                device.id,
                "info.protocol_type",
                ProtocolType.HTTP.value,
            )
        # Keep in-memory object in sync
        device.last_seen = now
        device.info.protocol_type = ProtocolType.HTTP
    except sqlite3.Error as e:
        logger.error(
            f"Failed to update device {device.id} last_seen and protocol_type: {e}"
        )

    return next_app_logic(db_conn, user, device)


@router.get("/{device_id}/brightness", name="get_brightness")
def get_brightness(deps: UserAndDevice = Depends(get_user_and_device)) -> Response:
    brightness_value = db.get_device_brightness_percent(deps.device)
    return Response(content=str(brightness_value), media_type="text/plain")


@router.get("/{device_id}/currentapp", name="currentwebp")
def currentwebp(
    request: Request, deps: UserAndDevice = Depends(get_user_and_device)
) -> Response:
    device = deps.device
    # first check for a pushed file starting with __ and just return that and then delete it.
    # This needs to happen before brightness check to clear any ephemeral images
    pushed_dir = db.get_device_webp_dir(device.id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            logger.debug(f"Found pushed image {ephemeral_file}")
            response = send_image(ephemeral_file, device, None, True)
            ephemeral_file.unlink()
            return response

    # If brightness is 0, short-circuit and return default image to save processing
    brightness = device.brightness or 50
    if brightness == 0:
        logger.debug("Brightness is 0, returning default image")
        return send_default_image(device)

    # Use helper function to get the app to display, without advancing the index
    app, _, is_pinned_app, is_night_mode_app, is_interstitial_app = _get_app_to_display(
        device, advance_index=False
    )

    if app is None:
        return send_default_image(device)

    # For pinned apps, always display them regardless of enabled/schedule status
    # For interstitial apps, always display them regardless of enabled/schedule status
    # For other apps, check if they should be displayed
    if (
        not is_pinned_app
        and not is_night_mode_app
        and not is_interstitial_app
        and (not app.enabled or not db.get_is_app_schedule_active(app, device))
    ):
        return send_default_image(device)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device.id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device.id) / f"{app.name}-{app.iname}.webp"

    try:
        stat_result = webp_path.stat()
    except FileNotFoundError:
        return send_default_image(device)

    if stat_result.st_size > 0:
        response = send_image(webp_path, device, app, stat_result=stat_result)
        etag = response.headers.get("etag")
        if_none_match = request.headers.get("if-none-match")
        if if_none_match and if_none_match == etag:
            return Response(status_code=status.HTTP_304_NOT_MODIFIED)
        return response
    else:
        return send_default_image(device)


@router.get("/{device_id}/{iname}/appwebp", name="appwebp")
def appwebp(iname: str, deps: UserAndDevice = Depends(get_user_and_device)) -> Response:
    device = deps.device
    app = device.apps.get(iname)
    if not app:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    if app.pushed:
        webp_path = db.get_device_webp_dir(device.id) / "pushed" / f"{app.iname}.webp"
    else:
        webp_path = db.get_device_webp_dir(device.id) / f"{app.name}-{app.iname}.webp"
    if webp_path.exists() and webp_path.stat().st_size > 0:
        return send_image(webp_path, device, app)
    else:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)


@router.post("/{device_id}/{iname}/schema_handler/{handler}", name="schema_handler")
async def schema_handler(
    request: Request,
    iname: str,
    handler: str,
    deps: UserAndDevice = Depends(get_user_and_device),
) -> Response:
    device = deps.device
    app = device.apps.get(iname)
    if not app or not app.path:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    try:
        data = await request.json()
        if "param" not in data:
            raise HTTPException(status_code=status.HTTP_400_BAD_REQUEST)

        config = data.get("config", {})
        result = call_handler_with_config(
            Path(app.path), config, handler, data["param"]
        )
        return Response(content=result, media_type="application/json")
    except Exception as e:
        logger.error(f"Error in schema_handler: {e}")
        raise HTTPException(status_code=status.HTTP_500_INTERNAL_SERVER_ERROR)


@router.post("/{device_id}/reorder_apps", name="reorder_apps")
def reorder_apps(
    device_id: DeviceID,
    dragged_iname: str = Form(...),
    target_iname: str = Form(...),
    insert_after: bool = Form(False),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Reorder apps by dragging and dropping."""
    device = user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    apps_dict = device.apps

    if dragged_iname not in apps_dict or target_iname not in apps_dict:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST, detail="App not found"
        )

    # Convert apps_dict to a list of app tuples (iname, app_data)
    apps_list = list(apps_dict.items())

    # Sort the list by app.order, then by iname for stability if orders are not unique
    apps_list.sort(key=lambda x: (getattr(x[1], "order", 0), x[0]))

    # Find the indices of the dragged and target apps
    dragged_idx = -1
    target_idx = -1

    for i, (app_iname, app_data) in enumerate(apps_list):
        if app_iname == dragged_iname:
            dragged_idx = i
        if app_iname == target_iname:
            target_idx = i

    if dragged_idx == -1 or target_idx == -1:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST, detail="App not found in list"
        )

    # Don't allow moving to the same position
    if dragged_idx == target_idx:
        return Response("OK", status_code=status.HTTP_200_OK)

    # Calculate the new position
    if insert_after:
        new_idx = target_idx + 1
    else:
        new_idx = target_idx

    # Adjust for the fact that we're removing the dragged item first
    if dragged_idx < new_idx:
        new_idx -= 1

    # Move the app
    moved_app = apps_list.pop(dragged_idx)
    apps_list.insert(new_idx, moved_app)

    # Update 'order' attribute for all apps
    logger.info(f"Reordering apps for device {device_id}")
    for i, (app_iname, app_data) in enumerate(apps_list):
        logger.info(f"Setting {app_iname} order to {i} (was {app_data.order})")
        app_data.order = i

    db.save_user(db_conn, user)
    logger.info("Saved user after reordering apps")

    return Response("OK", status_code=status.HTTP_200_OK)


@router.get("/health", name="health")
def health() -> Response:
    """Health check endpoint."""
    return Response(content="OK", status_code=status.HTTP_200_OK)
