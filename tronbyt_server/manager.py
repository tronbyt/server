import json
import logging
import os
import secrets
import select
import shutil
import string
import subprocess
import time
import uuid
from http import HTTPStatus
from operator import itemgetter
from pathlib import Path
from random import randint
from threading import Thread, Timer
from typing import Any, Dict, Optional, Tuple
from urllib.parse import urlencode
from zoneinfo import available_timezones

import psutil
import requests
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
    url_for,
)
from flask.typing import ResponseReturnValue
from tzlocal import get_localzone_name
from werkzeug.utils import secure_filename

import tronbyt_server.db as db
from tronbyt_server import get_schema
from tronbyt_server import render_app as pixlet_render_app
from tronbyt_server.auth import login_required
from tronbyt_server.models.app import App
from tronbyt_server.models.device import (
    DEFAULT_DEVICE_TYPE,
    Device,
    validate_device_id,
    validate_device_type,
)
from tronbyt_server.models.user import User

bp = Blueprint("manager", __name__)


@bp.route("/")
@login_required
def index() -> str:
    devices: list[Device] = list()

    if not g.user:
        current_app.logger.error("check [user].json file, might be corrupted")

    if "devices" in g.user:
        devices = list(reversed(list(g.user["devices"].values())))
    return render_template(
        "manager/index.html", devices=devices, server_root=server_root()
    )


# function to handle uploading a an app
@bp.route("/uploadapp", methods=["GET", "POST"])
@login_required
def uploadapp() -> ResponseReturnValue:
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
            filename = secure_filename(file.filename)
            if filename == "":
                flash("No file")
                return redirect("manager.uploadapp")

            # create a subdirectory for the app
            app_name = Path(filename).stem
            app_subdir = user_apps_path / app_name
            app_subdir.mkdir(parents=True, exist_ok=True)

            # save the file
            if db.save_user_app(file, app_subdir):
                flash("Upload Successful")
                return redirect(url_for("manager.index"))
            else:
                flash("Save Failed")
                return redirect(url_for("manager.uploadapp"))

    # check for existance of apps path
    user_apps_path.mkdir(parents=True, exist_ok=True)

    star_files = [file.name for file in user_apps_path.rglob("*.star")]

    return render_template("manager/uploadapp.html", files=star_files)


# function to delete an uploaded star file
@bp.route("/deleteupload/<string:filename>", methods=["POST", "GET"])
@login_required
def deleteupload(filename: str) -> ResponseReturnValue:
    db.delete_user_upload(g.user, filename)
    return redirect(url_for("manager.uploadapp"))


@bp.route("/adminindex")
@login_required
def adminindex() -> str:
    if g.user["username"] != "admin":
        abort(HTTPStatus.NOT_FOUND)
    userlist = list()
    # go through the users folder and build a list of all users
    users = os.listdir("users")
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
            device = Device(
                id=device_id,
                name=name or device_id,
                type=device_type,
                img_url=img_url,
                api_key=api_key,
                brightness=int(brightness) if brightness else 3,
                default_interval=10,
            )
            if notes:
                device["notes"] = notes
            current_app.logger.debug("device_id is :" + str(device["id"]))
            user = g.user
            user.setdefault("devices", {})[device["id"]] = device
            if db.save_user(user) and not db.get_device_webp_dir(device["id"]).is_dir():
                db.get_device_webp_dir(device["id"]).mkdir(parents=True)

            return redirect(url_for("manager.index"))
    return render_template("manager/create.html")


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
    user["devices"][device_id]["brightness"] = int(brightness)
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
        device_type = request.form.get("device_type")
        notes = request.form.get("notes")
        img_url = request.form.get("img_url")
        api_key = request.form.get("api_key")
        default_interval = request.form.get("default_interval")
        brightness = request.form.get("brightness")
        night_brightness = request.form.get("night_brightness")
        night_start = request.form.get("night_start")
        night_end = request.form.get("night_end")
        night_mode_app = request.form.get("night_mode_app")
        error = None
        if not name or not device_id:
            error = "Id and Name is required."
        if error is not None:
            flash(error)
        else:
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
            if device_type:
                if not validate_device_type(device_type):
                    abort(HTTPStatus.BAD_REQUEST, description="Invalid device type")
                device["type"] = device_type
            if api_key:
                device["api_key"] = api_key
            if notes:
                device["notes"] = notes
            if default_interval:
                device["default_interval"] = int(default_interval)
            if brightness:
                device["brightness"] = int(brightness)
            if night_brightness:
                device["night_brightness"] = int(night_brightness)
            if night_start:
                device["night_start"] = int(night_start)
            if night_end:
                device["night_end"] = int(night_end)
            if night_mode_app:
                device["night_mode_app"] = night_mode_app

            user = g.user
            if "apps" in user["devices"][device_id]:
                device["apps"] = user["devices"][device_id]["apps"]
            user["devices"][device_id] = device
            db.save_user(user)

            return redirect(url_for("manager.index"))
    device = g.user["devices"][device_id]
    return render_template(
        "manager/update.html",
        device=device,
        server_root=server_root(),
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
    users_dir = db.get_users_dir()
    device = g.user["devices"][device_id]
    app = device["apps"][iname]

    tmp_config_path = (
        users_dir / g.user["username"] / "configs" / f"{app['name']}-{app['iname']}.tmp"
    )

    if tmp_config_path.is_file():
        tmp_config_path.unlink()

    if "pushed" in app:
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
        app_details = db.get_app_details(g.user["username"], name)
        app_path = app_details.get("path")
        if app_path:
            app["path"] = app_path
        if uinterval:
            app["uinterval"] = int(uinterval)
        if display_time:
            app["display_time"] = int(display_time)
        if notes:
            app["notes"] = notes

        user = g.user
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


@bp.route("/<string:device_id>/<string:iname>/toggle_enabled", methods=["GET"])
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


@bp.route("/<string:device_id>/<string:iname>/preview", methods=["GET"])
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
        data = pixlet_render_app(
            path=app_path,
            config=config,
            width=64,
            height=32,
            magnify=1,
            maxDuration=15000,
            timeout=30000,
            image_format=0,  # 0 == WebP
            silence_output=current_app.config.get("PRODUCTION") == "1",
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


def render_app(
    app_path: Path, config: Dict[str, Any], webp_path: Path, device: Device
) -> Tuple[bool, bool]:
    """Renders a pixlet app to a webp image.

    Args:
        app_path: Path to the pixlet app.
        config: The app's configuration settings.
        webp_path: Path to save the rendered webp image.
        device: Device configuration.

    Returns:
        A tuple of two flags:
            - The first flag indicates whether the rendering was successful.
            - The second flag indicates whether the result was empty.
    """
    config_data = config.copy()  # Create a copy to avoid modifying the original config
    add_default_config(config_data, device)

    data = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=64,
        height=32,
        magnify=1,
        maxDuration=15000,
        timeout=30000,
        image_format=0,  # 0 == WebP
        silence_output=current_app.config.get("PRODUCTION") == "1",
    )
    if data is None:
        current_app.logger.error("Error running pixlet render")
        return False, False
    # leave the previous file in place if the new one is empty
    # this way, we still display the last successful render on the index page,
    # even if the app returns no screens
    if len(data) > 0:
        webp_path.write_bytes(data)
        return True, False
    return True, True


def server_root() -> str:
    protocol = current_app.config["SERVER_PROTOCOL"]
    hostname = current_app.config["SERVER_HOSTNAME"]
    port = current_app.config["MAIN_PORT"]
    url = f"{protocol}://{hostname}"
    if (protocol == "https" and port != "443") or (protocol == "http" and port != "80"):
        url += f":{port}"
    return url


# render if necessary, returns false on failure, true for all else
def possibly_render(user: User, device_id: str, app: App) -> bool:
    if "pushed" in app:
        current_app.logger.debug("Pushed App -- NO RENDER")
        return True
    now = int(time.time())
    app_basename = "{}-{}".format(app["name"], app["iname"])
    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app_basename}.webp"

    if "path" in app:
        app_path = Path(app["path"])
    else:
        # current_app.logger.debug("No path for {}, trying default location".format(app["name"]))
        app_path = (
            Path("system-apps/apps")
            / app["name"].replace("_", "")
            / f"{app['name']}.star"
        )

    if now - app.get("last_render", 0) > int(app["uinterval"]) * 60:
        current_app.logger.debug(f"RENDERING -- {app_basename}")
        device = user["devices"][device_id]
        config = app.get("config", {}).copy()
        add_default_config(config, device)
        success, empty = render_app(app_path, config, webp_path, device)
        if not success:
            current_app.logger.error(f"Error rendering {app_basename}")
        # set the empty flag in the app whilst keeping last render image
        app["empty_last_render"] = empty
        # always update the config with the new last render time
        app["last_render"] = now
        db.save_user(user)
        return success  # return false if error

    current_app.logger.debug(f"{app_basename} -- NO RENDER")
    return True


@bp.route("/<string:device_id>/firmware", methods=["POST", "GET"])
@login_required
def generate_firmware(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    # Ensure the device exists in the current user's configuration
    device = g.user["devices"].get(device_id, None)
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
            swap_colors = bool(request.form.get("swap_colors", False))

            # Pass the device type to the firmware generation function
            result = db.generate_firmware(
                label=secure_filename(device["name"]),
                url=image_url,
                ap=ap,
                pw=password,
                device_type=device_type,
                swap_colors=swap_colors,
            )
            if "file_path" in result:
                device["firmware_file_path"] = result["file_path"]
                db.save_user(g.user)
                return render_template(
                    "manager/firmware.html",
                    device=device,
                    img_url=image_url,
                    ap=ap,
                    password=password,
                    device_type=device_type,
                    firmware_file=result["file_path"],
                )
            elif "error" in result:
                flash(str(result["error"]))
            else:
                flash("Firmware modification failed")

    return render_template(
        "manager/firmware_form.html",
        device=device,
        server_root=server_root(),
    )


def add_default_config(config: Dict[str, Any], device: Device) -> Dict[str, Any]:
    if (
        "timezone" in device
        and isinstance(device["timezone"], str)
        and device["timezone"] != ""
    ):
        config["$tz"] = device["timezone"]
    else:
        localzone = get_localzone_name()
        if localzone:
            config["$tz"] = localzone
        else:
            current_app.logger.warning(
                "Could not determine local timezone, using UTC as default"
            )
            config["$tz"] = "Etc/UTC"
    return config


@bp.route(
    "/<string:device_id>/<string:iname>/<int:delete_on_cancel>/configapp",
    methods=["GET", "POST"],
)
@login_required
def configapp(device_id: str, iname: str, delete_on_cancel: int) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    users_dir = db.get_users_dir()
    # used when rendering configapp
    domain_host = current_app.config["SERVER_HOSTNAME"]
    protocol = current_app.config["SERVER_PROTOCOL"]

    device = g.user.get("devices", {}).get(device_id)
    if not device:
        abort(HTTPStatus.NOT_FOUND, description="Device not found")
    app = device.get("apps", {}).get(iname)
    if app is None:
        current_app.logger.error("couldn't get app iname {iname} from user {g.user}")
        flash("Error saving app, please try again.")
        return redirect(url_for("manager.addapp", device_id=device_id))
    app_basename = "{}-{}".format(app["name"], app["iname"])
    app_details = db.get_app_details(g.user["username"], app["name"])
    if "path" in app_details:
        app_path = Path(app_details["path"])
    else:
        app_path = (
            Path("system-apps")
            / "apps"
            / "{}/{}.star".format(app["name"].replace("_", ""), app["name"])
        )

    if os.getenv("USE_PIXLET_SCHEMA", "0") == "1":
        if request.method == "GET":
            schema_json = get_schema(app_path)
            schema = json.loads(schema_json) if schema_json else None
            return render_template(
                "manager/configapp.html",
                app=app,
                domain_host=domain_host,
                protocol=protocol,
                url_params=None,
                device_id=device_id,
                delete_on_cancel=delete_on_cancel,
                user_render_port=None,
                config=app.get("config", {}),
                schema=schema,
            )

        if request.method == "POST":
            config = request.form.to_dict()
            app["config"] = config
            db.save_user(g.user)

            # render the app with the new config to store the preview
            webp_device_path = db.get_device_webp_dir(device_id)
            webp_device_path.mkdir(parents=True, exist_ok=True)
            webp_path = webp_device_path / f"{app_basename}.webp"
            success, _ = render_app(app_path, config, webp_path, device)
            if success:
                # set the enabled key in app to true now that it has been configured.
                app["enabled"] = True
                # set last_rendered to seconds
                app["last_render"] = int(time.time())
                # always save
                db.save_user(g.user)
            else:
                flash("Error Rendering App")

            return redirect(url_for("manager.index"))

    user_render_port = str(db.get_user_render_port(g.user["username"]))
    # always kill the pixlet process based on port number.
    for proc in psutil.process_iter(["pid", "name", "cmdline"]):
        if (
            "pixlet" in proc.info["name"]
            and proc.info["cmdline"]
            and f"--port={user_render_port}" in proc.info["cmdline"]
        ):
            try:
                proc.terminate()
                try:
                    proc.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    proc.kill()
            except Exception as e:
                current_app.logger.error(f"Error terminating pixlet process: {e}")

    tmp_config_path = users_dir / g.user["username"] / "configs" / f"{app_basename}.tmp"
    if request.method == "POST":
        #  do something to confirm configuration ?
        webp_device_path = db.get_device_webp_dir(device_id)
        webp_device_path.mkdir(parents=True, exist_ok=True)
        webp_path = webp_device_path / f"{app_basename}.webp"
        current_app.logger.debug("checking for : " + str(tmp_config_path))
        if tmp_config_path.exists():
            current_app.logger.debug("file exists")
            with tmp_config_path.open("r") as c:
                new_config = json.load(c)
            # remove internal settings like `$tz` and `$watch` from the stored config
            new_config = {k: v for k, v in new_config.items() if not k.startswith("$")}
            app["config"] = new_config
            db.save_user(g.user)

            # save location as default if checked
            if request.form.get("location_as_default", None):
                if "location" in new_config:
                    device["location"] = new_config["location"]

            # delete the tmp file
            tmp_config_path.unlink()

            # run pixlet render with the new config
            success, _ = render_app(app_path, new_config, webp_path, device)
            if success:
                # set the enabled key in app to true now that it has been configured.
                app["enabled"] = True
                # set last_rendered to seconds
                app["last_render"] = int(time.time())
                # always save
                db.save_user(g.user)
            else:
                flash("Error Rendering App")

        return redirect(url_for("manager.index"))

    # run the in browser configure interface via pixlet serve
    elif request.method == "GET":
        if not app_path.exists():
            flash("App Not Found")
            return redirect(url_for("manager.index"))

        pixlet_path = Path(os.getenv("PIXLET_PATH", "/pixlet/pixlet"))
        if not pixlet_path.exists():
            flash("Pixlet binary not found")
            return redirect(url_for("manager.index"))

        config_dict = app.get("config", {}).copy()
        formatted_config = config_dict.copy()
        add_default_config(config_dict, device)
        config_dict["$watch"] = "false"
        url_params = urlencode(config_dict)

        # execute the pixlet serve process and show in it an iframe on the config page.
        current_app.logger.debug(str(app_path))
        p = subprocess.Popen(
            [
                str(pixlet_path),
                "--saveconfig",
                str(tmp_config_path),
                "serve",
                str(app_path),
                "--port={}".format(user_render_port),
                "--path=/pixlet/",
            ],
            stderr=subprocess.PIPE,
            stdout=subprocess.PIPE,
            shell=False,
            text=True,
            bufsize=1,
        )

        # Start a watchdog timer to kill the process after some time
        def terminate_process(
            logger: logging.Logger, p: subprocess.Popen[str], port: str
        ) -> None:
            logger.debug(f"terminating pixlet on port {port}")
            try:
                p.terminate()
                try:
                    p.wait(timeout=2)
                except subprocess.TimeoutExpired:
                    p.kill()
            except Exception as e:
                logger.error(f"Error terminating pixlet process: {e}")

        timer = Timer(
            float(os.getenv("PIXLET_TIMEOUT", "300")),
            terminate_process,
            [current_app.logger, p, user_render_port],
        )
        timer.start()

        # wait for pixlet to serve
        # first, wait for the process to emit a message to stderr ("listening at")
        # once that message is seen, wait for the health check to pass.
        if p.stderr:
            timeout = 10  # seconds
            start_time = time.time()
            saw_message = False
            while True:
                if time.time() - start_time > timeout:
                    current_app.logger.error("Timeout waiting for pixlet to start")
                    p.terminate()
                    try:
                        p.wait(timeout=2)
                    except subprocess.TimeoutExpired:
                        p.kill()
                    flash("Error: Timeout waiting for pixlet to start")
                    return redirect(url_for("manager.index"))

                if saw_message:
                    try:
                        if requests.get(
                            f"http://localhost:{user_render_port}/pixlet/health"
                        ).ok:
                            current_app.logger.debug(
                                f"pixlet ready on port {user_render_port}"
                            )
                            break
                    except requests.ConnectionError:
                        current_app.logger.debug(
                            f"pixlet not ready yet for port {user_render_port}, retrying"
                        )
                else:
                    ready = select.select([p.stderr], [], [], 1.0)
                    if ready[0]:
                        output = p.stderr.readline()
                        if "listening at" in output:
                            saw_message = True
                            continue
                time.sleep(0.1)

            def log_subprocess_output(pipe: Any, logger: logging.Logger) -> None:
                with pipe:
                    for line in iter(pipe.readline, ""):
                        logger.debug(line.strip())

            Thread(
                target=log_subprocess_output, args=(p.stderr, current_app.logger)
            ).start()
            Thread(
                target=log_subprocess_output, args=(p.stdout, current_app.logger)
            ).start()

        return render_template(
            "manager/configapp.html",
            app=app,
            domain_host=domain_host,
            protocol=protocol,
            url_params=url_params,
            device_id=device_id,
            delete_on_cancel=delete_on_cancel,
            user_render_port=user_render_port,
            config=formatted_config,
            schema=None,
        )
    abort(HTTPStatus.BAD_REQUEST)


@bp.route("/<string:device_id>/brightness", methods=["GET"])
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


@bp.route("/<string:device_id>/next")
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
        ephemeral_files = [f for f in pushed_dir.iterdir() if f.name.startswith("__")]
        if ephemeral_files:
            ephemeral_file = ephemeral_files[0]
            current_app.logger.debug(
                f"returning ephemeral pushed file {ephemeral_file.name}"
            )
            webp_path = pushed_dir / ephemeral_file.name
            # send_file doesn't need the full path, just the path after the tronbyt_server
            response = send_image(webp_path, device, None)
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
        not app["enabled"]
        or not db.get_is_app_schedule_active(app, device.get("timezone", None))
    ):
        # recurse until we find one that's enabled
        current_app.logger.debug(f"{app['name']}-{app['iname']} is disabled")
        return next_app(device_id, last_app_index, recursion_depth + 1)

    # render if necessary, returns false on failure, true for all else
    if not possibly_render(user, device_id, app) or app.get("empty_last_render", False):
        # try the next app if rendering failed or produced an empty result (no screens)
        return next_app(device_id, last_app_index, recursion_depth + 1)

    db.save_user(user)

    if "pushed" in app:
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
    webp_path: Path, device: Device, app: Optional[App]
) -> ResponseReturnValue:
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
@bp.route("/<string:device_id>/currentapp")
def currentwebp(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    try:
        user = g.user
        device = user["devices"][device_id]
        apps_list = sorted(device["apps"].values(), key=itemgetter("order"))
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


@bp.route("/<string:device_id>/<string:iname>/appwebp")
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

        if "pushed" in app:
            webp_path = (
                db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
            )
        else:
            webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"
        if webp_path.exists() and webp_path.stat().st_size > 0:
            return send_file(webp_path, mimetype="image/webp")
        else:
            current_app.logger.error("file doesn't exist or 0 size")
            abort(HTTPStatus.NOT_FOUND)
    except Exception as e:
        current_app.logger.error(f"Exception: {str(e)}")
        abort(HTTPStatus.NOT_FOUND)


@bp.route("/<string:device_id>/download_firmware")
@login_required
def download_firmware(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    try:
        if (
            g.user
            and device_id in g.user["devices"]
            and "firmware_file_path" in g.user["devices"][device_id]
        ):
            file_path = Path(g.user["devices"][device_id]["firmware_file_path"])
        else:
            abort(HTTPStatus.NOT_FOUND)

        current_app.logger.debug(f"checking for {file_path}")
        if file_path.exists() and file_path.stat().st_size > 0:
            return send_file(file_path, mimetype="application/octet-stream")
        else:
            current_app.logger.error("file doesn't exist or 0 size")
            abort(HTTPStatus.NOT_FOUND)
    except Exception as e:
        current_app.logger.error(f"Exception: {str(e)}")
        abort(HTTPStatus.NOT_FOUND)


def set_repo(repo_name: str, apps_path: Path, repo_url: str) -> bool:
    if repo_url != "":
        old_repo = g.user.get(repo_name, "")
        if old_repo != repo_url:
            # just get the last two words of the repo
            repo_url = "/".join(repo_url.split("/")[-2:])
            g.user[repo_name] = repo_url
            db.save_user(g.user)

            if apps_path.exists():
                shutil.rmtree(apps_path)
            result = subprocess.run(
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
            result = subprocess.run(["git", "-C", str(apps_path), "pull"])
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
            subprocess.run(["python3", "clone_system_apps_repo.py"])
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
            subprocess.run(["python3", "clone_system_apps_repo.py"])
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


@bp.route("/pixlet", defaults={"path": ""}, methods=["GET", "POST"])
@bp.route("/pixlet/<path:path>", methods=["GET", "POST"])
@login_required
def pixlet_proxy(path: str) -> ResponseReturnValue:
    user_render_port = db.get_user_render_port(g.user["username"])
    pixlet_url = f"http://localhost:{user_render_port}/pixlet/{path}?{request.query_string.decode()}"
    try:
        if request.method == "GET":
            response = requests.get(
                pixlet_url, params=request.args.to_dict(), headers=dict(request.headers)
            )
        elif request.method == "POST":
            response = requests.post(
                pixlet_url, data=request.get_data(), headers=dict(request.headers)
            )
        response.raise_for_status()
    except requests.exceptions.RequestException as e:
        current_app.logger.error(f"Error fetching {pixlet_url}: {e}")
        abort(HTTPStatus.INTERNAL_SERVER_ERROR)
    excluded_headers = [
        "Content-Length",
        "Transfer-Encoding",
        "Content-Encoding",
        "Connection",
    ]
    headers = [
        (name, value)
        for (name, value) in response.raw.headers.items()
        if name not in excluded_headers
    ]
    return Response(response.content, status=response.status_code, headers=headers)


@bp.route("/<string:device_id>/<string:iname>/moveapp", methods=["GET", "POST"])
@login_required
def moveapp(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    direction = request.args.get("direction")
    if not direction:
        return redirect(url_for("manager.index"))

    user = g.user
    apps = user["devices"][device_id]["apps"]
    app = apps[iname]
    if direction == "up":
        if app["order"] == 0:
            return redirect(url_for("manager.index"))
        app["order"] -= 1
    elif direction == "down":
        if app["order"] == len(apps) - 1:
            return redirect(url_for("manager.index"))
        app["order"] += 1
    apps[iname] = app

    # Ensure no two apps have the same order
    for other_iname, other_app in apps.items():
        if other_iname != iname and other_app["order"] == app["order"]:
            if direction == "up":
                other_app["order"] += 1
            elif direction == "down":
                other_app["order"] -= 1

    # Sort apps by order
    user["devices"][device_id]["apps"] = apps
    db.save_user(user)
    return redirect(url_for("manager.index"))


@bp.route("/health", methods=["GET"])
def health() -> ResponseReturnValue:
    return Response("OK", status=200)
