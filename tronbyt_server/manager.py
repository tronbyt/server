import json
import os
import secrets
import shutil
import string
import subprocess
import time
import uuid
from http import HTTPStatus
from io import BytesIO
from multiprocessing import Manager
from operator import itemgetter
from pathlib import Path
from random import randint
from threading import Lock
from typing import Any, Dict, Optional
from zoneinfo import available_timezones

from flask import (
    Blueprint,
    Response,
    abort,
    current_app,
    flash,
    g,
    redirect,
    render_template,
    request,
    send_file,
    send_from_directory,
    url_for,
)
from flask.typing import ResponseReturnValue
from flask_sock import Server as WebSocketServer
from werkzeug.utils import secure_filename

import tronbyt_server.db as db
from tronbyt_server import call_handler, get_schema, sock, system_apps
from tronbyt_server import render_app as pixlet_render_app
from tronbyt_server.auth import login_required
from tronbyt_server.models.app import App
from tronbyt_server.models.device import (
    DEFAULT_DEVICE_TYPE,
    Device,
    Location,
    device_supports_2x,
    validate_device_id,
    validate_device_type,
)
from tronbyt_server.models.user import User

bp = Blueprint("manager", __name__)


def git_command(
    command: list[str], cwd: Optional[Path] = None, check: bool = False
) -> subprocess.CompletedProcess[bytes]:
    """Run a git command in the specified path."""
    # ensure `HOME` is set because it's required by `git`
    env = os.environ.copy()
    env.setdefault("HOME", os.getcwd())
    return subprocess.run(command, cwd=cwd, env=env, check=check)


@bp.get("/")
@login_required
def index() -> str:
    devices: list[Device] = list()

    if not g.user:
        current_app.logger.error("check [user].json file, might be corrupted")

    if "devices" in g.user:
        # Get the devices and convert brightness values to UI scale for display
        devices = []
        for device in reversed(list(g.user["devices"].values())):
            ui_device = device.copy()
            if "brightness" in ui_device:
                ui_device["brightness"] = db.percent_to_ui_scale(
                    ui_device["brightness"]
                )
            if "night_brightness" in ui_device:
                ui_device["night_brightness"] = db.percent_to_ui_scale(
                    ui_device["night_brightness"]
                )
            devices.append(ui_device)

    return render_template("manager/index.html", devices=devices)


# function to handle uploading an app
@bp.route("/<string:device_id>/uploadapp", methods=["GET", "POST"])
@login_required
def uploadapp(device_id: str) -> ResponseReturnValue:
    user_apps_path = db.get_users_dir() / g.user["username"] / "apps"
    if request.method == "POST":
        # check if the post request has the file part
        if "file" not in request.files:
            flash("No file part")
            return redirect("manager.uploadapp")
        file = request.files["file"]
        # if user does not select file, browser also
        # submit an empty part without filename
        if file:
            if not file.filename:
                flash("No file")
                return redirect(url_for("manager.addapp", device_id=device_id))
            filename = secure_filename(file.filename)

            # create a subdirectory for the app
            app_name = Path(filename).stem
            app_subdir = user_apps_path / app_name
            app_subdir.mkdir(parents=True, exist_ok=True)

            # save the file
            if not db.save_user_app(file, app_subdir):
                flash("Save Failed")
                return redirect(url_for("manager.uploadapp", device_id=device_id))
            flash("Upload Successful")

            # try to generate a preview with an empty config (ignore errors)
            preview = db.get_data_dir() / "apps" / f"{app_name}.webp"
            render_app(app_subdir, {}, preview, Device(id=""), None)

            return redirect(url_for("manager.addapp", device_id=device_id))

    # check for existence of apps path
    user_apps_path.mkdir(parents=True, exist_ok=True)

    star_files = [file.name for file in user_apps_path.rglob("*.star")]

    return render_template(
        "manager/uploadapp.html", files=star_files, device_id=device_id
    )


# function to delete an uploaded star file
@bp.route("/<string:device_id>/deleteupload/<string:filename>", methods=["POST", "GET"])
@login_required
def deleteupload(device_id: str, filename: str) -> ResponseReturnValue:
    # Check if the file is use by any devices
    user = g.user
    if any(
        app_path.name == filename
        for device in user.get("devices", {}).values()
        for app in device.get("apps", {}).values()
        for app_path in [Path(app.get("path", ""))]
    ):
        flash(
            f"Cannot delete {filename} because it is installed on a device. To replace an app just re-upload the file."
        )
        return redirect(url_for("manager.addapp", device_id=device_id))

    # If not referenced, proceed with deletion
    db.delete_user_upload(g.user, filename)
    return redirect(url_for("manager.addapp", device_id=device_id))


@bp.get("/adminindex")
@login_required
def adminindex() -> str:
    if g.user["username"] != "admin":
        abort(HTTPStatus.NOT_FOUND)
    userlist = list()
    # go through the users folder and build a list of all users
    users = os.listdir(db.get_users_dir())
    # read in the user.config file
    for username in users:
        user = db.get_user(username)
        if user:
            userlist.append(user)

    return render_template("manager/adminindex.html", users=userlist)


@bp.post("/admin/<string:username>/deleteuser")
@login_required
def deleteuser(username: str) -> ResponseReturnValue:
    if g.user["username"] != "admin":
        abort(HTTPStatus.NOT_FOUND)
    if username != "admin":
        db.delete_user(username)
    return redirect(url_for("manager.adminindex"))


@bp.route("/create", methods=["GET", "POST"])
@login_required
def create() -> ResponseReturnValue:
    if request.method == "POST":
        name = request.form.get("name")
        device_type = request.form.get("device_type")
        img_url = request.form.get("img_url")
        api_key = request.form.get("api_key")
        notes = request.form.get("notes")
        brightness = request.form.get("brightness")
        locationJSON = request.form.get("location")
        error = None
        if not name or db.get_device_by_name(g.user, name):
            error = "Unique name is required."
        if error is not None:
            flash(error)
        else:
            # just use first 8 chars is good enough
            max_attempts = 10
            for _ in range(max_attempts):
                device_id = str(uuid.uuid4())[0:8]
                if device_id not in g.user.get("devices", {}):
                    break
            else:
                flash("Could not generate a unique device ID.")
                return redirect(url_for("manager.create"))
            if not img_url:
                img_url = f"{server_root()}/{device_id}/next"
            if not api_key or api_key == "":
                api_key = "".join(
                    secrets.choice(string.ascii_letters + string.digits)
                    for _ in range(32)
                )
            device_type = device_type or DEFAULT_DEVICE_TYPE
            if not validate_device_type(device_type):
                flash("Invalid device type")
                return redirect(url_for("manager.create"))
            # Convert UI scale brightness to percentage
            ui_brightness = int(brightness) if brightness else 3
            percent_brightness = db.ui_scale_to_percent(ui_brightness)

            device = Device(
                id=device_id,
                name=name or device_id,
                type=device_type,
                img_url=img_url,
                api_key=api_key,
                brightness=percent_brightness,  # Store as percentage
                default_interval=10,
            )
            #  This is duplicated coded from update function
            if locationJSON and locationJSON != "{}":
                try:
                    loc = json.loads(locationJSON)
                    lat = loc.get("lat", None)
                    lng = loc.get("lng", None)
                    if lat and lng:
                        location = Location(
                            lat=lat,
                            lng=lng,
                        )
                        if "name" in loc:
                            location["name"] = loc["name"]
                        if "timezone" in loc:
                            location["timezone"] = loc["timezone"]
                        device["location"] = location
                    else:
                        flash("Invalid location")
                        # abort(HTTPStatus.BAD_REQUEST, description="Invalid location")

                except json.JSONDecodeError as e:
                    flash(f"Location JSON error {e}")
            if notes:
                device["notes"] = notes
            current_app.logger.debug("device_id is :" + str(device["id"]))
            user = g.user
            user.setdefault("devices", {})[device["id"]] = device
            if db.save_user(user) and not db.get_device_webp_dir(device["id"]).is_dir():
                db.get_device_webp_dir(device["id"]).mkdir(parents=True)

            return redirect(url_for("manager.index"))
    return render_template("manager/create.html")


@bp.get("/app_preview/<string:filename>")
def app_preview(filename: str) -> ResponseReturnValue:
    return send_from_directory(db.get_data_dir() / "apps", filename)


@bp.post("/<string:device_id>/update_brightness")
@login_required
def update_brightness(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    if device_id not in g.user["devices"]:
        abort(HTTPStatus.NOT_FOUND)
    brightness = request.form.get("brightness")
    if brightness is None:
        abort(HTTPStatus.BAD_REQUEST)

    user = g.user
    device = user["devices"][device_id]

    # Convert UI scale (0-5) to percentage (0-100) and store only the percentage
    ui_brightness = int(brightness)
    device["brightness"] = db.ui_scale_to_percent(ui_brightness)

    db.save_user(user)
    return ""


@bp.post("/<string:device_id>/update_interval")
@login_required
def update_interval(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    if device_id not in g.user["devices"]:
        abort(HTTPStatus.NOT_FOUND)
    interval = request.form.get("interval")
    if interval is None:
        abort(HTTPStatus.BAD_REQUEST)
    g.user["devices"][device_id]["default_interval"] = int(interval)
    db.save_user(g.user)
    return ""


@bp.route("/<string:device_id>/update", methods=["GET", "POST"])
@login_required
def update(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    if device_id not in g.user["devices"]:
        abort(HTTPStatus.NOT_FOUND)
    if request.method == "POST":
        name = request.form.get("name")
        error = None
        if not name or not device_id:
            error = "Id and Name is required."
        if error is not None:
            flash(error)
        else:
            img_url = request.form.get("img_url")
            device = Device(
                id=device_id,
                night_mode_enabled=bool(request.form.get("night_mode_enabled")),
                timezone=str(request.form.get("timezone")),
                img_url=(
                    db.sanitize_url(img_url)
                    if img_url and len(img_url) > 0
                    else f"{server_root()}/{device_id}/next"
                ),
            )
            if name:
                device["name"] = name
            device_type = request.form.get("device_type")
            if device_type:
                if not validate_device_type(device_type):
                    abort(HTTPStatus.BAD_REQUEST, description="Invalid device type")
                device["type"] = device_type
            api_key = request.form.get("api_key")
            if api_key:
                device["api_key"] = api_key
            notes = request.form.get("notes")
            if notes:
                device["notes"] = notes
            default_interval = request.form.get("default_interval")
            if default_interval:
                device["default_interval"] = int(default_interval)
            brightness = request.form.get("brightness")
            if brightness:
                ui_brightness = int(brightness)
                device["brightness"] = db.ui_scale_to_percent(ui_brightness)
            night_brightness = request.form.get("night_brightness")
            if night_brightness:
                ui_night_brightness = int(night_brightness)
                device["night_brightness"] = db.ui_scale_to_percent(ui_night_brightness)
            night_start = request.form.get("night_start")
            if night_start:
                device["night_start"] = int(night_start)
            night_end = request.form.get("night_end")
            if night_end:
                device["night_end"] = int(night_end)
            night_mode_app = request.form.get("night_mode_app")
            if night_mode_app:
                device["night_mode_app"] = night_mode_app
            locationJSON = request.form.get("location")
            if locationJSON and locationJSON != "{}":
                try:
                    loc = json.loads(locationJSON)
                    lat = loc.get("lat", None)
                    lng = loc.get("lng", None)
                    if lat and lng:
                        location = Location(
                            lat=lat,
                            lng=lng,
                        )
                        if "name" in loc:
                            location["name"] = loc["name"]
                        if "timezone" in loc:
                            location["timezone"] = loc["timezone"]
                        device["location"] = location
                    else:
                        flash("Invalid location")
                        # abort(HTTPStatus.BAD_REQUEST, description="Invalid location")

                except json.JSONDecodeError as e:
                    flash(f"Location JSON error {e}")
            user = g.user
            if "apps" in user["devices"][device_id]:
                device["apps"] = user["devices"][device_id]["apps"]
            user["devices"][device_id] = device
            db.save_user(user)

            return redirect(url_for("manager.index"))
    device = g.user["devices"][device_id]
    device["ws_url"] = ws_root() + f"/{device_id}/ws"

    # Convert percentage brightness values to UI scale (0-5) for display
    ui_device = device.copy()
    if "brightness" in ui_device:
        ui_device["brightness"] = db.percent_to_ui_scale(ui_device["brightness"])
    if "night_brightness" in ui_device:
        ui_device["night_brightness"] = db.percent_to_ui_scale(
            ui_device["night_brightness"]
        )

    return render_template(
        "manager/update.html",
        device=ui_device,
        available_timezones=available_timezones(),
    )


@bp.post("/<string:device_id>/delete")
@login_required
def delete(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    device = g.user["devices"].get(device_id, None)
    if not device:
        abort(HTTPStatus.NOT_FOUND)
    g.user["devices"].pop(device_id)
    db.save_user(g.user)
    db.delete_device_dirs(device_id)
    return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/<string:iname>/delete", methods=["POST", "GET"])
@login_required
def deleteapp(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    device = g.user["devices"][device_id]
    app = device["apps"][iname]

    if app.get("pushed", False):
        webp_path = (
            db.get_device_webp_dir(device["id"]) / "pushed" / f"{app['name']}.webp"
        )
    else:
        webp_path = (
            db.get_device_webp_dir(device["id"]) / f"{app['name']}-{app['iname']}.webp"
        )

    if webp_path.is_file():
        webp_path.unlink()

    device["apps"].pop(iname)
    db.save_user(g.user)
    return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/addapp", methods=["GET", "POST"])
@login_required
def addapp(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    if device_id not in g.user["devices"]:
        abort(HTTPStatus.NOT_FOUND, description="Device not found")
    if request.method == "GET":
        # build the list of apps.
        custom_apps_list = db.get_apps_list(g.user["username"])
        apps_list = db.get_apps_list("system")
        return render_template(
            "manager/addapp.html",
            device=g.user["devices"][device_id],
            apps_list=apps_list,
            custom_apps_list=custom_apps_list,
        )

    elif request.method == "POST":
        name = request.form.get("name")
        uinterval = request.form.get("uinterval")
        display_time = request.form.get("display_time")
        notes = request.form.get("notes")

        if not name:
            flash("App name required.")
            return redirect(url_for("manager.addapp", device_id=device_id))

        # Generate a unique iname
        max_attempts = 10
        for _ in range(max_attempts):
            iname = str(randint(100, 999))
            if iname not in g.user["devices"][device_id].get("apps", {}):
                break
        else:
            flash("Could not generate a unique installation ID.")
            return redirect(
                url_for(
                    "manager.addapp",
                    device_id=device_id,
                )
            )

        app = App(
            name=name,
            iname=iname,
            enabled=False,  # start out false, only set to true after configure is finished
            last_render=0,
        )
        user = g.user
        app_details = db.get_app_details_by_name(user["username"], name)
        app_path = app_details.get("path")
        if app_path:
            app["path"] = app_path
        if uinterval:
            app["uinterval"] = int(uinterval)
        if display_time:
            app["display_time"] = int(display_time)
        if notes:
            app["notes"] = notes
        app_id = app_details.get("id")
        if app_id:
            app["id"] = app_id

        apps = user["devices"][device_id].setdefault("apps", {})
        app["order"] = len(apps)

        apps[iname] = app
        db.save_user(user)

        return redirect(
            url_for(
                "manager.configapp",
                device_id=device_id,
                iname=iname,
                delete_on_cancel=1,
            )
        )
    return Response("Method not allowed", 405)


@bp.get("/<string:device_id>/<string:iname>/toggle_enabled")
@login_required
def toggle_enabled(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    user = g.user
    app = user["devices"][device_id]["apps"][iname]
    app["enabled"] = not app["enabled"]
    # if enabled, we should probably re-render and push but that's a pain so not doing it right now.

    db.save_user(user)
    flash("Changes saved.")
    return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/<string:iname>/updateapp", methods=["GET", "POST"])
@login_required
def updateapp(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    if request.method == "POST":
        name = request.form.get("name")
        error = None
        if not name or not iname:
            error = "Name and installation_id is required."
        if error is not None:
            flash(error)
        else:
            uinterval = request.form.get("uinterval")
            notes = request.form.get("notes")
            enabled = "enabled" in request.form
            start_time = request.form.get("start_time")
            end_time = request.form.get("end_time")

            user = g.user
            app: App = user["devices"][device_id]["apps"][iname]
            app["iname"] = iname
            if name:
                app["name"] = name
            if uinterval:
                app["uinterval"] = int(uinterval)
            app["display_time"] = int(request.form.get("display_time", 0))
            if notes:
                app["notes"] = notes
            if start_time:
                app["start_time"] = start_time
            if end_time:
                app["end_time"] = end_time
            app["days"] = request.form.getlist("days")
            app["enabled"] = enabled
            db.save_user(user)

            return redirect(url_for("manager.index"))
    app = g.user["devices"][device_id]["apps"][iname]

    return render_template(
        "manager/updateapp.html",
        app=app,
        device_id=device_id,
        config=json.dumps(app.get("config", {}), indent=4),
    )


def add_default_config(config: Dict[str, Any], device: Device) -> Dict[str, Any]:
    config["$tz"] = db.get_device_timezone_str(device)
    return config


@bp.get("/<string:device_id>/<string:iname>/preview")
@login_required
def preview(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    device = g.user.get("devices", {}).get(device_id)
    if not device:
        abort(HTTPStatus.NOT_FOUND, description="Device not found")

    app = device.get("apps", {}).get(iname)
    if app is None:
        current_app.logger.error("couldn't get app iname {iname} from user {g.user}")
        abort(HTTPStatus.NOT_FOUND, description="App not found")

    config = json.loads(request.args.get("config", "{}"))
    add_default_config(config, device)

    app_path = Path(app["path"])
    if not app_path.exists():
        abort(HTTPStatus.NOT_FOUND, description="App path not found")

    try:
        data = render_app(
            app_path=app_path,
            config=config,
            webp_path=None,
            device=device,
            app=app,
        )
        if data is None:
            abort(
                HTTPStatus.INTERNAL_SERVER_ERROR,
                description="Error running pixlet render",
            )

        return Response(data, mimetype="image/webp")
    except Exception as e:
        current_app.logger.error(f"Error in preview: {e}")
        abort(HTTPStatus.INTERNAL_SERVER_ERROR, description="Error generating preview")


@bp.post("/<string:device_id>/<string:iname>/schema_handler/<string:handler>")
@login_required
def schema_handler(device_id: str, iname: str, handler: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    device = g.user.get("devices", {}).get(device_id)
    if not device:
        abort(HTTPStatus.NOT_FOUND, description="Device not found")

    app = device.get("apps", {}).get(iname)
    if app is None:
        current_app.logger.error("couldn't get app iname {iname} from user {g.user}")
        abort(HTTPStatus.NOT_FOUND, description="App not found")

    try:
        # Parse the JSON body
        data = request.get_json()
        if not data or "param" not in data:
            abort(HTTPStatus.BAD_REQUEST, description="Invalid request body")

        # Call the handler with the provided parameter
        result = call_handler(Path(app["path"]), handler, data["param"])

        # Return the result as JSON
        return Response(result, mimetype="application/json")
    except Exception as e:
        current_app.logger.error(f"Error in schema_handler: {e}")
        abort(HTTPStatus.INTERNAL_SERVER_ERROR, description="Handler execution failed")


def render_app(
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
    config_data = config.copy()  # Create a copy to avoid modifying the original config
    add_default_config(config_data, device)

    if not app_path.is_absolute():
        app_path = db.get_data_dir() / app_path

    # default: render at 1x
    magnify = 1
    width = 64
    height = 32

    if device_supports_2x(device):
        # if the device supports 2x rendering, we scale up the app image
        magnify = 2
        # ...except for the apps which support 2x natively where we use the original size
        if app and "id" in app:
            app_details = db.get_app_details_by_id(g.user["username"], app["id"])
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
        current_app.logger.error("Error running pixlet render")
        return None
    if messages is not None and app is not None:
        db.save_render_messages(device, app, messages)
    if current_app.config.get("PRODUCTION") != "1":
        current_app.logger.debug(f"{app_path}: {messages}")

    # leave the previous file in place if the new one is empty
    # this way, we still display the last successful render on the index page,
    # even if the app returns no screens
    if len(data) > 0 and webp_path:
        webp_path.write_bytes(data)
    return data


def server_root() -> str:
    protocol = current_app.config["SERVER_PROTOCOL"]
    hostname = current_app.config["SERVER_HOSTNAME"]
    port = current_app.config["MAIN_PORT"]
    url = f"{protocol}://{hostname}"
    if (protocol == "https" and port != "443") or (protocol == "http" and port != "80"):
        url += f":{port}"
    return url


def ws_root() -> str:
    server_protocol = current_app.config["SERVER_PROTOCOL"]
    protocol = "wss" if server_protocol == "https" else "ws"
    hostname = current_app.config["SERVER_HOSTNAME"]
    port = current_app.config["MAIN_PORT"]
    url = f"{protocol}://{hostname}"
    if (protocol == "wss" and port != "443") or (protocol == "ws" and port != "80"):
        url += f":{port}"
    return url


# render if necessary, returns false on failure, true for all else
def possibly_render(user: User, device_id: str, app: App) -> bool:
    if app.get("pushed", False):
        current_app.logger.debug("Pushed App -- NO RENDER")
        return True
    now = int(time.time())
    app_basename = "{}-{}".format(app["name"], app["iname"])
    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app_basename}.webp"
    app_path = Path(app["path"])

    if now - app.get("last_render", 0) > int(app["uinterval"]) * 60:
        current_app.logger.debug(f"RENDERING -- {app_basename}")
        device = user["devices"][device_id]
        config = app.get("config", {}).copy()
        add_default_config(config, device)
        image = render_app(app_path, config, webp_path, device, app)
        if image is None:
            current_app.logger.error(f"Error rendering {app_basename}")
        # set the empty flag in the app whilst keeping last render image
        app["empty_last_render"] = len(image) == 0 if image is not None else False
        # always update the config with the new last render time
        app["last_render"] = now
        db.save_user(user)
        return image is not None  # return false if error

    current_app.logger.debug(f"{app_basename} -- NO RENDER")
    return True


@bp.route("/<string:device_id>/firmware", methods=["POST", "GET"])
@login_required
def generate_firmware(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    # Ensure the device exists in the current user's configuration
    device: Device = g.user["devices"].get(device_id, None)
    if not device:
        abort(HTTPStatus.NOT_FOUND)

    if request.method == "POST":
        if "wifi_ap" in request.form and "wifi_password" in request.form:
            ap = request.form.get("wifi_ap")
            if not ap:
                abort(HTTPStatus.BAD_REQUEST)
            password = request.form.get("wifi_password")
            if not password:
                abort(HTTPStatus.BAD_REQUEST)
            image_url = request.form.get("img_url")
            if not image_url:
                abort(HTTPStatus.BAD_REQUEST)

            device_type = device.get("type", DEFAULT_DEVICE_TYPE)
            current_app.logger.info(f"device type is: {device_type}")
            swap_colors = bool(request.form.get("swap_colors", False))

            # Pass the device type to the firmware generation function
            try:
                firmware_data = db.generate_firmware(
                    url=image_url,
                    ap=ap,
                    pw=password,
                    device_type=device_type,
                    swap_colors=swap_colors,
                )
            except Exception as e:
                current_app.logger.error(f"Error generating firmware: {e}")
                flash("Firmware generation failed. Please try again.")
                return redirect(
                    url_for("manager.generate_firmware", device_id=device_id)
                )

            return Response(
                firmware_data,
                mimetype="application/octet-stream",
                headers={
                    "Content-Disposition": f"attachment;filename=firmware_{device_type}_{device_id}.bin"
                },
            )

    return render_template("manager/firmware.html", device=device)


@bp.route(
    "/<string:device_id>/<string:iname>/<int:delete_on_cancel>/configapp",
    methods=["GET", "POST"],
)
@login_required
def configapp(device_id: str, iname: str, delete_on_cancel: int) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    device = g.user.get("devices", {}).get(device_id)
    if not device:
        abort(HTTPStatus.NOT_FOUND, description="Device not found")
    app = device.get("apps", {}).get(iname)
    if app is None:
        current_app.logger.error("couldn't get app iname {iname} from user {g.user}")
        flash("Error saving app, please try again.")
        return redirect(url_for("manager.addapp", device_id=device_id))
    app_basename = "{}-{}".format(app["name"], app["iname"])
    app_path = Path(app["path"])

    if request.method == "GET":
        schema_json = get_schema(app_path)
        schema = json.loads(schema_json) if schema_json else None
        return render_template(
            "manager/configapp.html",
            app=app,
            device=device,
            delete_on_cancel=delete_on_cancel,
            config=app.get("config", {}),
            schema=schema,
        )

    if request.method == "POST":
        config = request.get_json()
        app["config"] = config
        db.save_user(g.user)

        # render the app with the new config to store the preview
        webp_device_path = db.get_device_webp_dir(device_id)
        webp_device_path.mkdir(parents=True, exist_ok=True)
        webp_path = webp_device_path / f"{app_basename}.webp"
        image = render_app(app_path, config, webp_path, device, app)
        if image is not None:
            # set the enabled key in app to true now that it has been configured.
            app["enabled"] = True
            # set last_rendered to seconds
            app["last_render"] = int(time.time())
            # always save
            db.save_user(g.user)
        else:
            flash("Error Rendering App")

        return redirect(url_for("manager.index"))

    abort(HTTPStatus.BAD_REQUEST)


@bp.get("/<string:device_id>/brightness")
def get_brightness(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    user = db.get_user_by_device_id(device_id)
    if not user:
        abort(HTTPStatus.NOT_FOUND)
    device = user["devices"][device_id]
    brightness_value = db.get_device_brightness_8bit(device)
    current_app.logger.debug(f"brightness value {brightness_value}")
    return Response(str(brightness_value), mimetype="text/plain")


@bp.get("/<string:device_id>/next")
def next_app(
    device_id: str,
    last_app_index: Optional[int] = None,
    recursion_depth: int = 0,
) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    user = db.get_user_by_device_id(device_id) or abort(HTTPStatus.NOT_FOUND)
    device = user["devices"][device_id] or abort(HTTPStatus.NOT_FOUND)

    # first check for a pushed file starting with __ and just return that and then delete it.
    pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
    if pushed_dir.is_dir():
        for ephemeral_file in sorted(pushed_dir.glob("__*")):
            current_app.logger.debug(
                f"returning ephemeral pushed file {ephemeral_file.name}"
            )
            # Use the `immediate` flag because we're going to delete the file right after
            response = send_image(ephemeral_file, device, None, True)
            current_app.logger.debug("removing ephemeral webp")
            ephemeral_file.unlink()
            return response

    if recursion_depth > len(device.get("apps", {})):
        current_app.logger.warning(
            "Maximum recursion depth exceeded, sending default image"
        )
        return send_default_image(device)

    if last_app_index is None:
        last_app_index = db.get_last_app_index(device_id)
        if last_app_index is None:
            abort(HTTPStatus.NOT_FOUND)

    apps = device.get("apps", {})
    # if no apps return default.webp
    if not apps:
        return send_default_image(device)

    apps_list = sorted(apps.values(), key=itemgetter("order"))
    is_night_mode_app = False
    if (
        db.get_night_mode_is_active(device)
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
        not app["enabled"] or not db.get_is_app_schedule_active(app, device)
    ):
        # recurse until we find one that's enabled
        current_app.logger.debug(f"{app['name']}-{app['iname']} is disabled")
        return next_app(device_id, last_app_index, recursion_depth + 1)

    # render if necessary, returns false on failure, true for all else
    if not possibly_render(user, device_id, app) or app.get("empty_last_render", False):
        # try the next app if rendering failed or produced an empty result (no screens)
        return next_app(device_id, last_app_index, recursion_depth + 1)
    db.save_user(user)

    if app.get("pushed", False):
        webp_path = (
            db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
        )
    else:
        app_basename = "{}-{}".format(app["name"], app["iname"])
        webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"
    current_app.logger.debug(str(webp_path))

    if webp_path.exists() and webp_path.stat().st_size > 0:
        response = send_image(webp_path, device, app)
        current_app.logger.debug(f"app index is {last_app_index}")
        db.save_last_app_index(device_id, last_app_index)
        return response

    current_app.logger.error(f"file {webp_path} not found")
    # run it recursively until we get a file.
    return next_app(device_id, last_app_index, recursion_depth + 1)


def send_default_image(device: Device) -> ResponseReturnValue:
    return send_image(Path("static/images/default.webp"), device, None)


def send_image(
    webp_path: Path, device: Device, app: Optional[App], immediate: bool = False
) -> ResponseReturnValue:
    if immediate:
        with webp_path.open("rb") as f:
            response = send_file(BytesIO(f.read()), mimetype="image/webp")
    else:
        response = send_file(webp_path, mimetype="image/webp")
    b = db.get_device_brightness_8bit(device)
    device_interval = device.get("default_interval", 5)
    s = app.get("display_time", device_interval) if app else device_interval
    if s == 0:
        s = device_interval
    response.headers["Tronbyt-Brightness"] = b
    response.headers["Tronbyt-Dwell-Secs"] = s
    current_app.logger.debug(f"brightness {b} -- dwell seconds {s}")
    return response


# manager.currentwebp
@bp.get("/<string:device_id>/currentapp")
def currentwebp(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    try:
        user = g.user
        device = user["devices"][device_id]
        apps_list = sorted(device.get("apps", {}).values(), key=itemgetter("order"))
        if not apps_list:
            return send_default_image(device)
        current_app_index = db.get_last_app_index(device_id) or 0
        if current_app_index >= len(apps_list):
            current_app_index = 0
        current_app_iname = apps_list[current_app_index]["iname"]
        return appwebp(device_id, current_app_iname)
    except Exception as e:
        current_app.logger.error(f"Exception: {str(e)}")
        abort(HTTPStatus.NOT_FOUND)


@bp.get("/<string:device_id>/<string:iname>/appwebp")
def appwebp(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    try:
        if g.user:
            user = g.user
        else:
            user = db.get_user("admin")
        app = user["devices"][device_id]["apps"][iname]

        app_basename = "{}-{}".format(app["name"], app["iname"])

        if app.get("pushed", False):
            webp_path = (
                db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
            )
        else:
            webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"
        if webp_path.exists() and webp_path.stat().st_size > 0:
            return send_file(webp_path, mimetype="image/webp")
        else:
            current_app.logger.error(f"file {webp_path} doesn't exist or 0 size")
            abort(HTTPStatus.NOT_FOUND)
    except Exception as e:
        current_app.logger.error(f"Exception: {str(e)}")
        abort(HTTPStatus.NOT_FOUND)


def set_repo(repo_name: str, apps_path: Path, repo_url: str) -> bool:
    if repo_url != "":
        old_repo = g.user.get(repo_name, "")
        if old_repo != repo_url:
            # just get the last two path components and sanitize them
            repo_url = "/".join(
                secure_filename(part) for part in repo_url.split("/")[-2:]
            )
            g.user[repo_name] = repo_url
            db.save_user(g.user)

            if apps_path.exists():
                shutil.rmtree(apps_path)
            result = git_command(
                [
                    "git",
                    "clone",
                    "--depth",
                    "1",
                    f"https://blah:blah@github.com/{repo_url}",
                    str(apps_path),
                ]
            )
            if result.returncode == 0:
                flash("Repo Cloned")
                return True
            else:
                flash("Error Cloning Repo")
                return False
        else:
            result = git_command(["git", "-C", str(apps_path), "pull"])
            if result.returncode == 0:
                flash("Repo Updated")
                return True
            else:
                flash("Repo Update Failed")
                return False
    else:
        flash("No Changes to Repo")
        return True


@bp.route("/set_user_repo", methods=["GET", "POST"])
@login_required
def set_user_repo() -> ResponseReturnValue:
    if request.method == "POST":
        if "app_repo_url" not in request.form:
            abort(HTTPStatus.BAD_REQUEST)
        repo_url = str(request.form.get("app_repo_url"))
        apps_path = db.get_users_dir() / g.user["username"] / "apps"
        if set_repo("app_repo_url", apps_path, repo_url):
            return redirect(url_for("manager.index"))
        return redirect(url_for("auth.edit"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/set_api_key", methods=["GET", "POST"])
@login_required
def set_api_key() -> ResponseReturnValue:
    if request.method == "POST":
        if "api_key" not in request.form:
            abort(HTTPStatus.BAD_REQUEST)
        api_key = str(request.form.get("api_key"))
        if not api_key:
            flash("API Key cannot be empty.")
            return redirect(url_for("auth.edit"))
        g.user["api_key"] = api_key
        db.save_user(g.user)
        return redirect(url_for("manager.index"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/set_system_repo", methods=["GET", "POST"])
@login_required
def set_system_repo() -> ResponseReturnValue:
    if request.method == "POST":
        if g.user["username"] != "admin":
            abort(HTTPStatus.FORBIDDEN)
        if "app_repo_url" not in request.form:
            abort(HTTPStatus.BAD_REQUEST)
        repo_url = str(request.form.get("app_repo_url"))
        if set_repo("system_repo_url", Path("system-apps"), repo_url):
            # run the generate app list for custom repo
            # will just generate json file if already there.
            system_apps.update_system_repo(db.get_data_dir())
            return redirect(url_for("manager.index"))
        return redirect(url_for("auth.edit"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/refresh_system_repo", methods=["GET", "POST"])
@login_required
def refresh_system_repo() -> ResponseReturnValue:
    if request.method == "POST":
        if g.user["username"] != "admin":
            abort(HTTPStatus.FORBIDDEN)
        if set_repo(
            "system_repo_url", Path("system-apps"), g.user.get("system_repo_url", "")
        ):
            # run the generate app list for custom repo
            # will just generate json file if already there.
            system_apps.update_system_repo(db.get_data_dir())
            return redirect(url_for("manager.index"))
        return redirect(url_for("auth.edit"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/refresh_user_repo", methods=["GET", "POST"])
@login_required
def refresh_user_repo() -> ResponseReturnValue:
    if request.method == "POST":
        apps_path = db.get_users_dir() / g.user["username"] / "apps"
        if set_repo("app_repo_url", apps_path, g.user.get("app_repo_url", "")):
            return redirect(url_for("manager.index"))
        return redirect(url_for("auth.edit"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/<string:device_id>/<string:iname>/moveapp", methods=["GET", "POST"])
@login_required
def moveapp(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    direction = request.args.get("direction")
    if not direction or direction not in ["up", "down"]:
        flash("Invalid direction.")
        return redirect(url_for("manager.index"))

    user = g.user
    apps_dict = user["devices"][device_id].get("apps")

    if not apps_dict:
        # No apps to move
        return redirect(url_for("manager.index"))

    # Convert apps_dict to a list of app dictionaries, including iname
    apps_list = []
    for app_iname, app_data in apps_dict.items():
        app_item = app_data.copy()
        app_item["iname"] = app_iname
        apps_list.append(app_item)

    # Sort the list by 'order', then by 'iname' for stability if orders are not unique
    apps_list.sort(key=lambda x: (x.get("order", 0), x.get("iname", "")))

    # Find the current index of the app to be moved
    current_idx = -1
    for i, app_item in enumerate(apps_list):
        if app_item["iname"] == iname:
            current_idx = i
            break

    if current_idx == -1:
        # App to move not found in the list, should not happen if iname is valid
        flash("App not found.")
        return redirect(url_for("manager.index"))

    # Determine target index and perform boundary checks
    if direction == "up":
        if current_idx == 0:
            # Already at the top
            return redirect(url_for("manager.index"))
        target_idx = current_idx - 1
    else:  # direction == "down"
        if current_idx == len(apps_list) - 1:
            # Already at the bottom
            return redirect(url_for("manager.index"))
        target_idx = current_idx + 1

    # Move the app
    moved_app = apps_list.pop(current_idx)
    apps_list.insert(target_idx, moved_app)

    # Update 'order' attribute for all apps in the list and in the original apps_dict
    for i, app_item in enumerate(apps_list):
        app_item["order"] = i  # Update order in the list item
        apps_dict[app_item["iname"]]["order"] = i  # Update order in the original dict

    user["devices"][device_id]["apps"] = apps_dict
    db.save_user(user)

    return redirect(url_for("manager.index"))


@bp.get("/health")
def health() -> ResponseReturnValue:
    return Response("OK", status=200)


@bp.route("/<string:device_id>/export_config", methods=["GET", "POST"])
@login_required
def export_device_config(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    user = g.user
    device = user["devices"][device_id]

    # Convert the device dictionary to JSON
    device_json = json.dumps(device, indent=4)

    # Create a response to serve the JSON as a file download
    response = Response(
        device_json,
        mimetype="application/json",
        headers={"Content-Disposition": f"attachment;filename={device_id}_config.json"},
    )
    return response


@bp.route("/<string:device_id>/update_config", methods=["GET", "POST"])
@login_required
def import_device_config(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    if request.method == "POST":
        # Check if the POST request has the file part
        if "file" not in request.files:
            flash("No file part")
            return redirect(
                url_for("manager.import_device_config", device_id=device_id)
            )

        file = request.files["file"]

        # If no file is selected
        if not file.filename:
            flash("No selected file")
            return redirect(
                url_for("manager.import_device_config", device_id=device_id)
            )

        # Ensure the uploaded file is a JSON file
        if not file.filename.endswith(".json"):
            flash("Invalid file type. Please upload a JSON file.")
            return redirect(
                url_for("manager.import_device_config", device_id=device_id)
            )

        try:
            # Parse the JSON file
            device_config = json.load(file)

            # Validate the JSON structure (basic validation)
            if not isinstance(device_config, dict):
                flash("Invalid JSON structure")
                return redirect(
                    url_for("manager.import_device_config", device_id=device_id)
                )

            # Check if the device already exists
            user = g.user
            if (
                device_config["id"] not in user["devices"]
                and device_config["id"] == device_id
            ):
                flash("Not the same device id. Import skipped.")
                return redirect(url_for("manager.index"))

            device_config["img_url"] = f"{server_root()}/{device_id}/next"
            # Add the new device to the user's devices
            user["devices"][device_config["id"]] = device_config
            db.save_user(user)

            flash("Device configuration imported successfully")
            return redirect(url_for("manager.index"))

        except json.JSONDecodeError as e:
            flash(f"Error parsing JSON file: {e}")
            return redirect(
                url_for("manager.import_device_config", device_id=device_id)
            )

    # Render the import form
    return render_template("manager/import_config.html", device_id=device_id)


@bp.route("/import_device", methods=["GET", "POST"])
@login_required
def import_device() -> ResponseReturnValue:
    if request.method == "POST":
        # Check if the POST request has the file part
        if "file" not in request.files:
            flash("No file part")
            return redirect(url_for("manager.import_device"))

        file = request.files["file"]

        # If no file is selected
        if not file.filename:
            flash("No selected file")
            return redirect(url_for("manager.import_device"))

        # Ensure the uploaded file is a JSON file
        if not file.filename.endswith(".json"):
            flash("Invalid file type. Please upload a JSON file.")
            return redirect(url_for("manager.import_device"))

        try:
            # Parse the JSON file
            device_config = json.load(file)

            # Validate the JSON structure (basic validation)
            if not isinstance(device_config, dict):
                flash("Invalid JSON structure")
                return redirect(url_for("manager.create"))

            # Check if the device already exists
            user = g.user
            if device_config["id"] in user.get("devices", {}) or db.get_device_by_id(
                device_config["id"]
            ):
                flash("Device already exists. Import skipped.")
                return redirect(url_for("manager.index"))

            device = device_config
            device["img_url"] = f"{server_root()}/{device_config['id']}/next"

            # Add the new device to the user's devices
            user.setdefault("devices", {})[device_config["id"]] = device
            if db.save_user(user) and not db.get_device_webp_dir(device["id"]).is_dir():
                db.get_device_webp_dir(device["id"]).mkdir(parents=True)
            # probably want to call a db function actually create the device.
            db.save_user(user)

            flash("Device configuration imported successfully")
            return redirect(url_for("manager.index"))

        except json.JSONDecodeError as e:
            flash(f"Error parsing JSON file: {e}")
            return redirect(url_for("manager.import_device_config"))

    # Render the import form
    return render_template("manager/import_config.html")


# Use a Manager to create a shared dictionary for events across processes
manager = Manager()
device_conditions = manager.dict()
device_locks = Lock()


# Ignore untyped decorator: https://github.com/miguelgrinberg/flask-sock/issues/55
@sock.route("/<string:device_id>/ws")  # type: ignore
def websocket_endpoint(ws: WebSocketServer, device_id: str) -> None:
    if not validate_device_id(device_id):
        ws.close()
        return

    user = db.get_user_by_device_id(device_id)
    if not user:
        ws.close()
        return

    device = user["devices"].get(device_id)
    if not device:
        ws.close()
        return

    with device_locks:
        if device_id in device_conditions:
            device_condition = device_conditions[device_id]
        else:
            device_condition = manager.Condition()
            device_conditions[device_id] = device_condition

    dwell_time = device.get("default_interval", 5)
    last_brightness = None

    try:
        while ws.connected:
            try:
                response = next_app(device_id)
            except Exception as e:
                current_app.logger.error(f"Error in next_app: {e}")
                continue

            if isinstance(response, Response):
                if response.status_code == 200:
                    response.direct_passthrough = False  # Disable passthrough mode

                    # Send brightness as a text message, if it has changed
                    # This must be done before sending the image so that the new value is applied to the next image
                    brightness = response.headers.get("Tronbyt-Brightness")
                    if brightness is not None:
                        brightness_value = int(brightness)
                        if brightness_value != last_brightness:
                            last_brightness = brightness_value
                            ws.send(
                                json.dumps(
                                    {
                                        "brightness": brightness_value,
                                    }
                                )
                            )

                    # Send the image as a binary message
                    ws.send(bytes(response.get_data()))

                    # Update the dwell time based on the response header
                    dwell_secs = response.headers.get("Tronbyt-Dwell-Secs")
                    if dwell_secs:
                        dwell_time = int(dwell_secs)
                else:
                    ws.send(
                        json.dumps(
                            {
                                "status": "error",
                                "message": f"Error fetching image: {response.status_code}",
                            }
                        )
                    )
            else:
                ws.send(
                    json.dumps(
                        {
                            "status": "error",
                            "message": "Error fetching image, unknown response type",
                        }
                    )
                )

            # Wait for the next event or timeout
            with device_condition:
                device_condition.wait(timeout=dwell_time)
    except Exception as e:
        current_app.logger.error(f"WebSocket error: {e}")
        ws.close()


def push_new_image(device_id: str) -> None:
    """Wake up one WebSocket loop to push a new image."""
    with device_locks:
        if device_id in device_conditions:
            with device_conditions[device_id]:
                device_conditions[device_id].notify(1)
