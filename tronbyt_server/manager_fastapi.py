import asyncio
import json
import os
import secrets
import string
import time
import uuid
from http import HTTPStatus
from operator import itemgetter
from pathlib import Path
from random import randint
from typing import Any, Dict, Optional
from zoneinfo import available_timezones

from fastapi import APIRouter, Cookie, Depends, Form, Request, WebSocket, HTTPException
from fastapi.responses import (
    FileResponse,
    HTMLResponse,
    RedirectResponse,
    Response,
)
from werkzeug.utils import secure_filename

from tronbyt_server import db_fastapi as db
from tronbyt_server.connection_manager import manager
from tronbyt_server.templating import templates
from tronbyt_server.main import (
    logger,
    pixlet_render_app,
)
from tronbyt_server.pixlet_utils import call_handler, get_schema
from tronbyt_server.models.device import (
    DEFAULT_DEVICE_TYPE,
    device_supports_2x,
    validate_device_id,
    validate_device_type,
)
from tronbyt_server.models_fastapi import App, Device, Location, User
from tronbyt_server.auth_fastapi import login_required

router = APIRouter()


@router.get("/", response_class=HTMLResponse, name="index")
async def index(request: Request, current_user: User = Depends(login_required)):
    devices = []
    if current_user.devices:
        for device in reversed(list(current_user.devices.values())):
            ui_device = device.model_copy()
            if ui_device.brightness is not None:
                ui_device.brightness = db.percent_to_ui_scale(ui_device.brightness)
            if ui_device.night_brightness is not None:
                ui_device.night_brightness = db.percent_to_ui_scale(
                    ui_device.night_brightness
                )
            devices.append(ui_device)

    return templates.TemplateResponse(
        request, "manager/index.html", {"request": request, "devices": devices}
    )


@router.websocket("/{device_id}/ws")
async def websocket_endpoint(websocket: WebSocket, device_id: str):
    if not validate_device_id(device_id):
        await websocket.close()
        return

    user = db.get_user_by_device_id(logger, device_id)
    if not user:
        await websocket.close()
        return

    device = user["devices"].get(device_id)
    if not device:
        await websocket.close()
        return

    await manager.connect(websocket, device_id)
    dwell_time = device.get("default_interval", 5)
    last_brightness = None

    try:
        while True:
            try:
                response = await next_app(device_id)
            except Exception as e:
                print(f"Error in next_app: {e}")
                continue

            if isinstance(response, Response):
                if response.status_code == 200:
                    # Send brightness as a text message, if it has changed
                    brightness = response.headers.get("Tronbyt-Brightness")
                    if brightness is not None:
                        brightness_value = int(brightness)
                        if brightness_value != last_brightness:
                            last_brightness = brightness_value
                            await websocket.send_text(
                                json.dumps(
                                    {
                                        "brightness": brightness_value,
                                    }
                                )
                            )

                    # Send the image as a binary message
                    await websocket.send_bytes(response.body)

                    # Update the dwell time based on the response header
                    dwell_secs = response.headers.get("Tronbyt-Dwell-Secs")
                    if dwell_secs:
                        dwell_time = int(dwell_secs)
                else:
                    await websocket.send_text(
                        json.dumps(
                            {
                                "status": "error",
                                "message": f"Error fetching image: {response.status_code}",
                            }
                        )
                    )
            else:
                await websocket.send_text(
                    json.dumps(
                        {
                            "status": "error",
                            "message": "Error fetching image, unknown response type",
                        }
                    )
                )

            # Wait for the next event or timeout
            try:
                await asyncio.wait_for(
                    manager.active_connections[device_id].receive_text(),
                    timeout=dwell_time,
                )
            except asyncio.TimeoutError:
                pass
            except KeyError:
                break  # Connection closed
    except Exception as e:
        print(f"WebSocket error: {e}")
    finally:
        manager.disconnect(device_id)


async def push_new_image(device_id: str):
    """Wake up one WebSocket loop to push a new image."""
    await manager.send_personal_message("new_image", device_id)


async def next_app(
    device_id: str,
    last_app_index: Optional[int] = None,
    recursion_depth: int = 0,
) -> Response:
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    user = db.get_user_by_device_id(logger, device_id)
    if not user:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    device = user["devices"][device_id]
    if not device:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    # first check for a pushed file starting with __ and just return that and then delete it.
    pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            # Use the `immediate` flag because we're going to delete the file right after
            response = send_image(logger, ephemeral_file, device, None, True)
            ephemeral_file.unlink()
            return response

    if recursion_depth > len(device.get("apps", {})):
        return send_default_image(logger, device)

    if last_app_index is None:
        last_app_index = db.get_last_app_index(logger, device_id)
        if last_app_index is None:
            raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    apps = device.get("apps", {})
    # if no apps return default.webp
    if not apps:
        return send_default_image(device)

    apps_list = sorted(apps.values(), key=itemgetter("order"))
    is_night_mode_app = False
    if (
        db.get_night_mode_is_active(logger, device)
        and device.get("night_mode_app", "") in apps.keys()
    ):
        app = apps[device["night_mode_app"]]
        is_night_mode_app = True
    elif last_app_index + 1 < len(apps_list):  # will +1 be in bounds of array ?
        app = apps_list[last_app_index + 1]  # add 1 to get the next app
        last_app_index += 1
    else:
        app = apps_list[0]  # go to the beginning
        last_app_index = 0

    if not is_night_mode_app and (
        not app["enabled"]
        or not db.get_is_app_schedule_active(logger, app, device)
    ):
        # recurse until we find one that's enabled
        return await next_app(device_id, last_app_index, recursion_depth + 1)

    # render if necessary, returns false on failure, true for all else
    if not possibly_render(
        logger, User(**user), device_id, App(**app)
    ) or app.get("empty_last_render", False):
        # try the next app if rendering failed or produced an empty result (no screens)
        return await next_app(device_id, last_app_index, recursion_depth + 1)

    if app.get("pushed", False):
        webp_path = (
            db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
        )
    else:
        app_basename = "{}-{}".format(app["name"], app["iname"])
        webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"

    if webp_path.exists() and webp_path.stat().st_size > 0:
        response = send_image(logger, webp_path, device, app)
        db.save_last_app_index(logger, device_id, last_app_index)
        return response

    # run it recursively until we get a file.
    return await next_app(device_id, last_app_index, recursion_depth + 1)


def send_default_image(logger, device: dict) -> Response:
    return send_image(
        logger, Path("tronbyt_server/static/images/default.webp"), Device(**device), None
    )


def send_image(
    logger, webp_path: Path, device: dict, app: Optional[dict], immediate: bool = False
) -> Response:
    with open(webp_path, "rb") as f:
        content = f.read()

    b = db.get_device_brightness_8bit(logger, device)
    device_interval = device.get("default_interval", 5)
    s = app.get("display_time", device_interval) if app else device_interval
    if s == 0:
        s = device_interval

    headers = {
        "Tronbyt-Brightness": str(b),
        "Tronbyt-Dwell-Secs": str(s),
    }

    return Response(content=content, media_type="image/webp", headers=headers)


def add_default_config(config: Dict[str, Any], device: Device) -> Dict[str, Any]:
    config["$tz"] = db.get_device_timezone_str(device.model_dump())
    return config


def render_app(
    logger,
    app_path: Path,
    config: Dict[str, Any],
    webp_path: Optional[Path],
    device: Device,
    app: Optional[App],
) -> Optional[bytes]:
    """Renders a pixlet app to a webp image.

    Args:
        app_path: Path to the pixlet app.
        config: The app's configuration settings.
        webp_path: Path to save the rendered webp image.
        device: Device configuration.
        app: The application configuration.

    Returns: The rendered image as bytes or None if rendering fails.
    """
    if not pixlet_render_app:
        logger.warning("pixlet_render_app not available, skipping render")
        return None

    config_data = config.copy()  # Create a copy to avoid modifying the original config
    add_default_config(config_data, device)

    if not app_path.is_absolute():
        app_path = db.get_data_dir() / app_path

    # default: render at 1x
    magnify = 1
    width = 64
    height = 32

    if device_supports_2x(device.model_dump()):
        # if the device supports 2x rendering, we scale up the app image
        magnify = 2
        # ...except for the apps which support 2x natively where we use the original size
        if app and "id" in app:
            user = db.get_user_by_device_id(logger, device["id"])
            if user:
                app_details = db.get_app_details_by_id(
                    logger, user["username"], app["id"]
                )
                if app_details.get("supports2x", False):
                    magnify = 1
                    width = 128
                    height = 64

    data, messages = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=width,
        height=height,
        magnify=magnify,
        maxDuration=15000,
        timeout=30000,
        image_format=0,  # 0 == WebP
    )
    # Ensure messages is always a list
    messages = (
        messages if isinstance(messages, list) else [messages] if messages else []
    )

    if data is None:
        print("Error running pixlet render")
        return None
    if messages is not None and app is not None:
        db.save_render_messages(
            logger, device.model_dump(), app.model_dump(), messages
        )

    # leave the previous file in place if the new one is empty
    # this way, we still display the last successful render on the index page,
    # even if the app returns no screens
    if len(data) > 0 and webp_path:
        webp_path.write_bytes(data)
    return data


def possibly_render(logger, user: User, device_id: str, app: App) -> bool:
    if app.pushed:
        return True
    now = int(time.time())
    app_basename = "{}-{}".format(app.name, app.iname)
    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app_basename}.webp"
    app_path = Path(app.path)

    if now - app.last_render > int(app.uinterval) * 60:
        device = user.devices[device_id]
        config = app.config.copy()
        add_default_config(config, device)
        image = render_app(logger, app_path, config, webp_path, device, app)
        if image is None:
            print(f"Error rendering {app_basename}")
        # set the empty flag in the app whilst keeping last render image
        app.empty_last_render = len(image) == 0 if image is not None else False
        # always update the config with the new last render time
        app.last_render = now
        # we save app here in case empty_last_render is set, it needs saving and next app won't have a chance to save it
        db.save_app(logger, device_id, app.model_dump())

        return image is not None  # return false if error

    return True


@router.route("/{device_id}/uploadapp", methods=["GET", "POST"], name="uploadapp")
async def uploadapp(
    request: Request,
    device_id: str,
    current_user: User = Depends(login_required),
):
    user_apps_path = db.get_users_dir() / current_user.username / "apps"
    if request.method == "POST":
        form = await request.form()
        file = form.get("file")
        if not file or not file.filename:
            return RedirectResponse(
                url=request.url_for("addapp", device_id=device_id),
                status_code=HTTPStatus.SEE_OTHER,
            )
        filename = secure_filename(file.filename)
        app_name = Path(filename).stem
        app_subdir = user_apps_path / app_name
        app_subdir.mkdir(parents=True, exist_ok=True)
        if not db.save_user_app(file, app_subdir):
            # flash("Save Failed")
            return RedirectResponse(
                url=request.url_for("uploadapp", device_id=device_id),
                status_code=HTTPStatus.SEE_OTHER,
            )
        # flash("Upload Successful")
        preview = db.get_data_dir() / "apps" / f"{app_name}.webp"
        render_app(logger, app_subdir, {}, preview, Device(id=""), None)
        return RedirectResponse(
            url=request.url_for("addapp", device_id=device_id),
            status_code=HTTPStatus.SEE_OTHER,
        )

    user_apps_path.mkdir(parents=True, exist_ok=True)
    star_files = [file.name for file in user_apps_path.rglob("*.star")]
    return templates.TemplateResponse(
        request,
        "manager/uploadapp.html",
        {"request": request, "files": star_files, "device_id": device_id},
    )


@router.route("/{device_id}/deleteupload/{filename}", methods=["POST", "GET"], name="deleteupload")
async def deleteupload(
    request: Request,
    device_id: str,
    filename: str,
    current_user: User = Depends(login_required),
):
    # Check if the file is use by any devices
    if any(
        Path(app.path).name == filename
        for device in current_user.devices.values()
        for app in device.apps.values()
    ):
        # flash(f"Cannot delete {filename} because it is installed on a device. To replace an app just re-upload the file.")
        return RedirectResponse(
            url=request.url_for("addapp", device_id=device_id),
            status_code=HTTPStatus.SEE_OTHER,
        )

    # If not referenced, proceed with deletion
    db.delete_user_upload(logger, current_user.model_dump(), filename)
    return RedirectResponse(
        url=request.url_for("addapp", device_id=device_id),
        status_code=HTTPStatus.SEE_OTHER,
    )


@router.get("/adminindex", response_class=HTMLResponse)
async def adminindex(
    request: Request, current_user: User = Depends(login_required)
):
    if current_user.username != "admin":
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    userlist = []
    users = os.listdir(db.get_users_dir())
    for username in users:
        user = db.get_user(logger, username)
        if user:
            userlist.append(user)
    return templates.TemplateResponse(
        request, "manager/adminindex.html", {"request": request, "users": userlist}
    )


@router.post("/admin/{username}/deleteuser")
async def deleteuser(
    request: Request, username: str, current_user: User = Depends(login_required)
):
    if current_user.username != "admin":
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    if username != "admin":
        db.delete_user(logger, username)
    return RedirectResponse(
        url=request.url_for("adminindex"), status_code=HTTPStatus.SEE_OTHER
    )


def server_root(request: Request) -> str:
    return str(request.base_url)


def ws_root(request: Request) -> str:
    return str(request.base_url).replace("http", "ws")


@router.get("/create", response_class=HTMLResponse)
async def create_get(request: Request, current_user: User = Depends(login_required)):
    return templates.TemplateResponse(
        request, "manager/create.html", {"request": request, "user": current_user}
    )


@router.post("/create")
async def create_post(request: Request, current_user: User = Depends(login_required)):
    form = await request.form()
    name = form.get("name")
    device_type = form.get("device_type")
    img_url = form.get("img_url")
    ws_url = form.get("ws_url")
    api_key = form.get("api_key")
    notes = form.get("notes")
    brightness = form.get("brightness")
    locationJSON = form.get("location")
    error = None
    if not name or db.get_device_by_name(
        logger, current_user.model_dump(), name
    ):
        error = "Unique name is required."
    if error is not None:
        # flash(error)
        pass
    else:
        max_attempts = 10
        for _ in range(max_attempts):
            device_id = str(uuid.uuid4())[0:8]
            if device_id not in current_user.devices:
                break
        else:
            # flash("Could not generate a unique device ID.")
            return RedirectResponse(
                url=request.url_for("create_get"), status_code=HTTPStatus.SEE_OTHER
            )
        if not img_url:
            img_url = f"{server_root(request)}{device_id}/next"
        if not ws_url:
            ws_url = f"{ws_root(request)}{device_id}/ws"

        if not api_key or api_key == "":
            api_key = "".join(
                secrets.choice(string.ascii_letters + string.digits)
                for _ in range(32)
            )
        device_type = device_type or DEFAULT_DEVICE_TYPE
        if not validate_device_type(device_type):
            # flash("Invalid device type")
            return RedirectResponse(
                url=request.url_for("create_get"), status_code=HTTPStatus.SEE_OTHER
            )
        ui_brightness = int(brightness) if brightness else 3
        percent_brightness = db.ui_scale_to_percent(ui_brightness)

        device = Device(
            id=device_id,
            name=name or device_id,
            type=device_type,
            img_url=img_url,
            ws_url=ws_url,
            api_key=api_key,
            brightness=percent_brightness,
            default_interval=10,
        )
        if locationJSON and locationJSON != "{}":
            try:
                loc = json.loads(locationJSON)
                lat = loc.get("lat")
                lng = loc.get("lng")
                if lat and lng:
                    location = Location(
                        lat=lat,
                        lng=lng,
                    )
                    if "name" in loc:
                        location.name = loc["name"]
                    if "timezone" in loc:
                        location.timezone = loc["timezone"]
                    device.location = location
                else:
                    # flash("Invalid location")
                    pass
            except json.JSONDecodeError as e:
                # flash(f"Location JSON error {e}")
                pass
        if notes:
            device.notes = notes
        user = current_user.model_dump()
        user.setdefault("devices", {})[device.id] = device.model_dump()
        if db.save_user(logger, user) and not db.get_device_webp_dir(device.id).is_dir():
            db.get_device_webp_dir(device.id).mkdir(parents=True)

        return RedirectResponse(
            url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
        )
    return RedirectResponse(
        url=request.url_for("create_get"), status_code=HTTPStatus.SEE_OTHER
    )


@router.get("/app_preview/{filename}")
async def app_preview(filename: str):
    return FileResponse(db.get_data_dir() / "apps" / filename)


@router.post("/{device_id}/update_brightness")
async def update_brightness(
    device_id: str,
    brightness: int = Form(...),
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    if device_id not in current_user.devices:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    if brightness is None:
        raise HTTPException(status_code=HTTPStatus.BAD_REQUEST)

    user = current_user.model_dump()
    device = user["devices"][device_id]
    device["brightness"] = db.ui_scale_to_percent(brightness)
    db.save_user(logger, user)
    return ""


@router.route("/{device_id}/update", methods=["GET", "POST"])
async def update(
    request: Request,
    device_id: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    if device_id not in current_user.devices:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    if request.method == "POST":
        form = await request.form()
        name = form.get("name")
        error = None
        if not name or not device_id:
            error = "Id and Name is required."
        if error is not None:
            # flash(error)
            pass
        else:
            img_url = form.get("img_url")
            ws_url = form.get("ws_url")
            device = Device(
                id=device_id,
                night_mode_enabled=bool(form.get("night_mode_enabled")),
                timezone=str(form.get("timezone")),
                img_url=(
                    db.sanitize_url(img_url)
                    if img_url and len(img_url) > 0
                    else f"{server_root(request)}{device_id}/next"
                ),
                ws_url=(
                    db.sanitize_url(ws_url)
                    if ws_url and len(ws_url) > 0
                    else f"{ws_root(request)}{device_id}/ws"
                ),
            )
            if name:
                device.name = name
            device_type = form.get("device_type")
            if device_type:
                if not validate_device_type(device_type):
                    raise HTTPException(
                        status_code=HTTPStatus.BAD_REQUEST,
                        detail="Invalid device type",
                    )
                device.type = device_type
            api_key = form.get("api_key")
            if api_key:
                device.api_key = api_key
            notes = form.get("notes")
            if notes:
                device.notes = notes
            default_interval = form.get("default_interval")
            if default_interval:
                device.default_interval = int(default_interval)
            brightness = form.get("brightness")
            if brightness:
                ui_brightness = int(brightness)
                device.brightness = db.ui_scale_to_percent(ui_brightness)
            night_brightness = form.get("night_brightness")
            if night_brightness:
                ui_night_brightness = int(night_brightness)
                device.night_brightness = db.ui_scale_to_percent(
                    ui_night_brightness
                )
            night_start = form.get("night_start")
            if night_start:
                device.night_start = int(night_start)
            night_end = form.get("night_end")
            if night_end:
                device.night_end = int(night_end)
            night_mode_app = form.get("night_mode_app")
            if night_mode_app:
                device.night_mode_app = night_mode_app
            locationJSON = form.get("location")
            if locationJSON and locationJSON != "{}":
                try:
                    loc = json.loads(locationJSON)
                    lat = loc.get("lat")
                    lng = loc.get("lng")
                    if lat and lng:
                        location = Location(
                            lat=lat,
                            lng=lng,
                        )
                        if "name" in loc:
                            location.name = loc["name"]
                        if "timezone" in loc:
                            location.timezone = loc["timezone"]
                        device.location = location
                    else:
                        # flash("Invalid location")
                        pass

                except json.JSONDecodeError as e:
                    # flash(f"Location JSON error {e}")
                    pass
            user = current_user.model_dump()
            if "apps" in user["devices"][device_id]:
                device.apps = user["devices"][device_id]["apps"]
            user["devices"][device_id] = device.model_dump()
            db.save_user(logger, user)

            return RedirectResponse(
                url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
            )

    device = current_user.devices[device_id]
    default_img_url = f"{server_root(request)}{device_id}/next"
    default_ws_url = f"{ws_root(request)}{device_id}/ws"

    ui_device = device.model_copy()
    if ui_device.brightness is not None:
        ui_device.brightness = db.percent_to_ui_scale(ui_device.brightness)
    if ui_device.night_brightness is not None:
        ui_device.night_brightness = db.percent_to_ui_scale(
            ui_device.night_brightness
        )

    return templates.TemplateResponse(
        request,
        "manager/update.html",
        {
            "request": request,
            "device": ui_device,
            "available_timezones": available_timezones(),
            "default_img_url": default_img_url,
            "default_ws_url": default_ws_url,
        },
    )


@router.get("/{device_id}/{iname}/toggle_enabled")
async def toggle_enabled(
    request: Request,
    device_id: str,
    iname: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    user = current_user.model_dump()
    app = user["devices"][device_id]["apps"][iname]
    app["enabled"] = not app["enabled"]
    db.save_user(logger, user)
    # flash("Changes saved.")
    return RedirectResponse(
        url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
    )


@router.route("/{device_id}/{iname}/updateapp", methods=["GET", "POST"])
async def updateapp(
    request: Request,
    device_id: str,
    iname: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    if request.method == "POST":
        form = await request.form()
        name = form.get("name")
        error = None
        if not name or not iname:
            error = "Name and installation_id is required."
        if error is not None:
            # flash(error)
            pass
        else:
            uinterval = form.get("uinterval")
            notes = form.get("notes")
            enabled = "enabled" in form
            start_time = form.get("start_time")
            end_time = form.get("end_time")

            user = current_user.model_dump()
            app = user["devices"][device_id]["apps"][iname]
            app["iname"] = iname
            if name:
                app["name"] = name
            if uinterval is not None:
                app["uinterval"] = int(uinterval)
            app["display_time"] = int(form.get("display_time", 0))
            if notes:
                app["notes"] = notes
            if start_time:
                app["start_time"] = start_time
            if end_time:
                app["end_time"] = end_time
            app["days"] = form.getlist("days")
            app["enabled"] = enabled
            db.save_user(logger, user)

            return RedirectResponse(
                url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
            )
    app = current_user.devices[device_id].apps[iname]
    return templates.TemplateResponse(
        request,
        "manager/updateapp.html",
        {
            "request": request,
            "app": app,
            "device_id": device_id,
            "config": json.dumps(app.config, indent=4),
        },
    )


@router.get("/{device_id}/{iname}/preview")
async def preview(
    request: Request,
    device_id: str,
    iname: str,
    config: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    device = current_user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device not found"
        )

    app = device.apps.get(iname)
    if app is None:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail="App not found")

    config_data = json.loads(config)
    add_default_config(config_data, device)

    app_path = Path(app.path)
    if not app_path.exists():
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="App path not found"
        )

    try:
        data = render_app(
            logger,
            app_path=app_path,
            config=config_data,
            webp_path=None,
            device=device,
            app=app,
        )
        if data is None:
            raise HTTPException(
                status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
                detail="Error running pixlet render",
            )

        return Response(data, media_type="image/webp")
    except Exception as e:
        print(f"Error in preview: {e}")
        raise HTTPException(
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
            detail="Error generating preview",
        )


@router.post("/{device_id}/{iname}/schema_handler/{handler}")
async def schema_handler(
    device_id: str,
    iname: str,
    handler: str,
    data: dict,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    device = current_user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device not found"
        )

    app = device.apps.get(iname)
    if app is None:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail="App not found")

    try:
        if not data or "param" not in data:
            raise HTTPException(
                status_code=HTTPStatus.BAD_REQUEST, detail="Invalid request body"
            )

        if not call_handler:
            raise HTTPException(status_code=HTTPStatus.NOT_IMPLEMENTED, detail="Pixlet not available")

        result = call_handler(Path(app.path), handler, data["param"])
        return Response(result, media_type="application/json")
    except Exception as e:
        print(f"Error in schema_handler: {e}")
        raise HTTPException(
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
            detail="Handler execution failed",
        )


@router.route("/{device_id}/{iname}/{delete_on_cancel}/configapp", methods=["GET", "POST"], name="configapp")
async def configapp(
    request: Request,
    device_id: str,
    iname: str,
    delete_on_cancel: int,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    device = current_user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device not found"
        )
    app = device.apps.get(iname)
    if app is None:
        # flash("Error saving app, please try again.")
        return RedirectResponse(
            url=request.url_for("addapp", device_id=device_id),
            status_code=HTTPStatus.SEE_OTHER,
        )
    app_basename = f"{app.name}-{app.iname}"
    app_path = Path(app.path)

    if request.method == "GET":
        if not get_schema:
            schema = None
        else:
            schema_json = get_schema(app_path)
            schema = json.loads(schema_json) if schema_json else None
        return templates.TemplateResponse(
            request,
            "manager/configapp.html",
            {
                "request": request,
                "app": app,
                "device": device,
                "delete_on_cancel": delete_on_cancel,
                "config": app.config,
                "schema": schema,
            },
        )

    if request.method == "POST":
        config = await request.json()
        user = current_user.model_dump()
        app.config = config
        user["devices"][device_id]["apps"][iname] = app.model_dump()
        db.save_user(logger, user)

        webp_device_path = db.get_device_webp_dir(device_id)
        webp_device_path.mkdir(parents=True, exist_ok=True)
        webp_path = webp_device_path / f"{app_basename}.webp"
        image = render_app(
            logger,
            app_path,
            config,
            webp_path,
            Device(**device.model_dump()),
            App(**app.model_dump()),
        )
        if image is not None:
            app.enabled = True
            app.last_render = int(time.time())
            db.save_app(logger, device_id, app.model_dump())
        else:
            # flash("Error Rendering App")
            pass

        return RedirectResponse(
            url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
        )

    raise HTTPException(status_code=HTTPStatus.BAD_REQUEST)


@router.post("/{device_id}/delete", name="delete_device")
async def delete(
    request: Request,
    device_id: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    device = current_user.devices.get(device_id)
    if not device:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    user = current_user.model_dump()
    user["devices"].pop(device_id)
    db.save_user(logger, user)
    db.delete_device_dirs(logger, device_id)
    return RedirectResponse(
        url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
    )


@router.route("/{device_id}/addapp", methods=["GET", "POST"], name="addapp")
async def addapp(
    request: Request,
    device_id: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    if device_id not in current_user.devices:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device not found"
        )

    if request.method == "POST":
        form = await request.form()
        name = form.get("name")
        uinterval = form.get("uinterval")
        display_time = form.get("display_time")
        notes = form.get("notes")

        if not name:
            # flash("App name required.")
            return RedirectResponse(
                url=request.url_for("addapp", device_id=device_id),
                status_code=HTTPStatus.SEE_OTHER,
            )

        max_attempts = 10
        for _ in range(max_attempts):
            iname = str(randint(100, 999))
            if iname not in current_user.devices[device_id].apps:
                break
        else:
            # flash("Could not generate a unique installation ID.")
            return RedirectResponse(
                url=request.url_for("addapp", device_id=device_id),
                status_code=HTTPStatus.SEE_OTHER,
            )

        app = App(
            name=name,
            iname=iname,
            enabled=False,
            last_render=0,
        )
        user = current_user.model_dump()
        app_details = db.get_app_details_by_name(
            logger, user["username"], name
        )
        app_path = app_details.get("path")
        if "recommended_interval" in app_details:
            uinterval = str(app_details["recommended_interval"])
        if app_path:
            app.path = app_path
        if uinterval is not None and uinterval != "":
            app.uinterval = int(uinterval)
        if display_time:
            app.display_time = int(display_time)
        if notes:
            app.notes = notes
        app_id = app_details.get("id")
        if app_id:
            app.id = app_id

        apps = user["devices"][device_id].setdefault("apps", {})
        app.order = len(apps)
        apps[iname] = app.model_dump()
        db.save_user(logger, user)

        return RedirectResponse(
            url=request.url_for(
                "configapp",
                device_id=device_id,
                iname=iname,
                delete_on_cancel=1,
            ),
            status_code=HTTPStatus.SEE_OTHER,
        )

    custom_apps_list = db.get_apps_list(logger, current_user.username)
    apps_list = db.get_apps_list(logger, "system")
    installed_app_names = set()
    for device in current_user.devices.values():
        installed_app_names.update(app.name for app in device.apps.values())
    apps_list.sort(key=lambda app: app["name"] not in installed_app_names)

    return templates.TemplateResponse(
        request,
        "manager/addapp.html",
        {
            "request": request,
            "device": current_user.devices[device_id],
            "apps_list": apps_list,
            "custom_apps_list": custom_apps_list,
        },
    )


@router.route("/{device_id}/{iname}/delete", methods=["POST", "GET"], name="deleteapp")
async def deleteapp(
    request: Request,
    device_id: str,
    iname: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    device = current_user.devices[device_id]
    app = device.apps[iname]

    if app.pushed:
        webp_path = (
            db.get_device_webp_dir(device.id) / "pushed" / f"{app.name}.webp"
        )
    else:
        webp_path = (
            db.get_device_webp_dir(device.id) / f"{app.name}-{app.iname}.webp"
        )

    if webp_path.is_file():
        webp_path.unlink()

    user = current_user.model_dump()
    user["devices"][device_id]["apps"].pop(iname)
    db.save_user(logger, user)
    return RedirectResponse(
        url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
    )


@router.post("/{device_id}/update_interval")
async def update_interval(
    device_id: str,
    interval: int = Form(...),
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    if device_id not in current_user.devices:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    if interval is None:
        raise HTTPException(status_code=HTTPStatus.BAD_REQUEST)
    user = current_user.model_dump()
    user["devices"][device_id]["default_interval"] = interval
    db.save_user(logger, user)
    return ""


@router.get("/{device_id}/next")
async def get_next_app_for_device(device_id: str):
    return await next_app(device_id)


@router.get("/{device_id}/current.webp", name="currentwebp")
async def currentwebp(device_id: str):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    return FileResponse(db.get_device_webp_path(device_id))


@router.get("/{device_id}/{iname}.webp", name="appwebp")
async def appwebp(device_id: str, iname: str):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    user = db.get_user_by_device_id(logger, device_id)
    if not user:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    device = user["devices"][device_id]
    if not device:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    app = device["apps"][iname]
    if not app:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    if app.get("pushed", False):
        webp_path = (
            db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
        )
    else:
        app_basename = "{}-{}".format(app["name"], app["iname"])
        webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"

    if not webp_path.exists():
        return send_default_image(logger, device)

    return FileResponse(webp_path)


@router.route("/{device_id}/firmware", methods=["GET", "POST"], name="generate_firmware")
async def generate_firmware(
    request: Request,
    device_id: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    device = current_user.devices.get(device_id)
    if not device:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device not found"
        )

    if request.method == "POST":
        form = await request.form()
        img_url = form.get("img_url")
        wifi_ap = form.get("wifi_ap")
        wifi_password = form.get("wifi_password")
        swap_colors = "swap_colors" in form

        if not all([img_url, wifi_ap, wifi_password]):
            # flash("All fields are required.")
            return templates.TemplateResponse(
                request, "manager/firmware.html", {"request": request, "device": device}
            )

        try:
            firmware = db.generate_firmware(
                img_url, wifi_ap, wifi_password, device.type, swap_colors
            )
            return Response(
                firmware,
                media_type="application/octet-stream",
                headers={
                    "Content-Disposition": f"attachment; filename={device.type}.bin"
                },
            )
        except ValueError as e:
            # flash(str(e))
            return templates.TemplateResponse(
                request, "manager/firmware.html", {"request": request, "device": device}
            )

    return templates.TemplateResponse(
        request, "manager/firmware.html", {"request": request, "device": device}
    )


@router.get("/{device_id}/{iname}/move", name="moveapp")
async def moveapp(
    request: Request,
    device_id: str,
    iname: str,
    direction: str,
    current_user: User = Depends(login_required),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )
    user = current_user.model_dump()
    apps = user["devices"][device_id]["apps"]
    apps_list = sorted(apps.values(), key=lambda app: app["order"])
    app_to_move = next((app for app in apps_list if app["iname"] == iname), None)

    if not app_to_move:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail="App not found")

    current_order = app_to_move["order"]
    if direction == "down":
        new_order = current_order + 1
    elif direction == "up":
        new_order = current_order - 1
    else:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid direction"
        )

    # Find the app to swap with
    app_to_swap = next(
        (app for app in apps_list if app["order"] == new_order), None
    )

    if app_to_swap:
        # Swap orders
        apps[app_to_move["iname"]]["order"] = new_order
        apps[app_to_swap["iname"]]["order"] = current_order

        db.save_user(logger, user)

    return RedirectResponse(
        url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
    )
