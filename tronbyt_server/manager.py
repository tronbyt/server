"""Manager blueprint for handling device and app management routes."""

import json
import os
import secrets
import shutil
import string
import subprocess
import time
import uuid
from datetime import date, timedelta
from http import HTTPStatus
from io import BytesIO
from html import escape
from operator import itemgetter
from pathlib import Path
from random import randint
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
from tronbyt_server import firmware_utils, sock, system_apps
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
from tronbyt_server.pixlet import call_handler, get_schema
from tronbyt_server.pixlet import render_app as pixlet_render_app
from tronbyt_server.sync import get_sync_manager

bp = Blueprint("manager", __name__)


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
        user_updated = False
        for device in reversed(list(g.user["devices"].values())):
            # Ensure ws_url is set for all devices (migration for existing devices)
            if "ws_url" not in device:
                device["ws_url"] = ws_root() + f"/{device['id']}/ws"
                user_updated = True

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

        # Save user data if any device was updated
        if user_updated:
            db.save_user(g.user)

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
        ws_url = request.form.get("ws_url")
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
            if not ws_url:
                ws_url = ws_root() + f"/{device_id}/ws"

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
                ws_url=ws_url,
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
                        if "locality" in loc:
                            location["locality"] = loc["locality"]
                        if "description" in loc:
                            location["description"] = loc["description"]
                        if "place_id" in loc:
                            location["place_id"] = loc["place_id"]
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
    # Validate brightness is in range 0-5
    if ui_brightness < 0 or ui_brightness > 5:
        abort(HTTPStatus.BAD_REQUEST, description="Brightness must be between 0 and 5")
    device["brightness"] = db.ui_scale_to_percent(ui_brightness)

    db.save_user(user)

    # Push an ephemeral brightness test image to the device (but not when brightness is 0)
    if ui_brightness > 0:
        try:
            push_brightness_test_image(device_id)
        except Exception as e:
            current_app.logger.error(f"Failed to push brightness test image: {e}")
            # Don't fail the brightness update if the test image fails

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
            ws_url = request.form.get("ws_url")
            device = Device(
                id=device_id,
                night_mode_enabled=bool(request.form.get("night_mode_enabled")),
                timezone=str(request.form.get("timezone")),
                img_url=(
                    db.sanitize_url(img_url)
                    if img_url and len(img_url) > 0
                    else f"{server_root()}/{device_id}/next"
                ),
                ws_url=(
                    db.sanitize_url(ws_url)
                    if ws_url and len(ws_url) > 0
                    else ws_root() + f"/{device_id}/ws"
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
                try:
                    device["night_start"] = parse_time_input(night_start)
                except ValueError as e:
                    flash(f"Invalid Night Start Time: {e}")
            night_end = request.form.get("night_end")
            if night_end:
                try:
                    device["night_end"] = parse_time_input(night_end)
                except ValueError as e:
                    flash(f"Invalid Night End Time: {e}")
            night_mode_app = request.form.get("night_mode_app")
            if night_mode_app:
                device["night_mode_app"] = night_mode_app

            # Handle dim time and dim brightness
            # Note: Dim mode ends at night_end time (if set) or 6:00 AM by default
            dim_time = request.form.get("dim_time")
            if dim_time and dim_time.strip():
                try:
                    device["dim_time"] = parse_time_input(dim_time)
                except ValueError as e:
                    flash(f"Invalid Dim Time: {e}")
            elif "dim_time" in device:
                # Remove dim_time if the field is empty
                del device["dim_time"]

            dim_brightness = request.form.get("dim_brightness")
            if dim_brightness:
                ui_dim_brightness = int(dim_brightness)
                device["dim_brightness"] = db.ui_scale_to_percent(ui_dim_brightness)

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
                        if "locality" in loc:
                            location["locality"] = loc["locality"]
                        if "description" in loc:
                            location["description"] = loc["description"]
                        if "place_id" in loc:
                            location["place_id"] = loc["place_id"]
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

    # Set default values for img_url and ws_url for "reset to default" function
    default_img_url = f"{server_root()}/{device_id}/next"
    default_ws_url = ws_root() + f"/{device_id}/ws"

    # Convert percentage brightness values to UI scale (0-5) for display
    ui_device = device.copy()
    if "brightness" in ui_device:
        ui_device["brightness"] = db.percent_to_ui_scale(ui_device["brightness"])
    if "night_brightness" in ui_device:
        ui_device["night_brightness"] = db.percent_to_ui_scale(
            ui_device["night_brightness"]
        )
    if "dim_brightness" in ui_device:
        ui_device["dim_brightness"] = db.percent_to_ui_scale(
            ui_device["dim_brightness"]
        )

    # Convert legacy integer time format to HH:MM for display
    if "night_start" in ui_device and isinstance(ui_device["night_start"], int):
        ui_device["night_start"] = f"{ui_device['night_start']:02d}:00"
    if "night_end" in ui_device and isinstance(ui_device["night_end"], int):
        ui_device["night_end"] = f"{ui_device['night_end']:02d}:00"

    return render_template(
        "manager/update.html",
        device=ui_device,
        available_timezones=available_timezones(),
        default_img_url=default_img_url,
        default_ws_url=default_ws_url,
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

        # Get the list of apps already installed on all devices for this user
        installed_app_names: set[str] = set()
        for device in g.user.get("devices", {}).values():
            installed_app_names.update(
                app["name"] for app in device.get("apps", {}).values()
            )

        # Mark installed apps and sort so that installed apps appear first
        for app_metadata in apps_list:
            app_metadata["is_installed"] = app_metadata["name"] in installed_app_names

        # Also mark installed status for custom apps
        for app_metadata in custom_apps_list:
            app_metadata["is_installed"] = app_metadata["name"] in installed_app_names

        # Sort apps_list so that installed apps appear first
        apps_list.sort(key=lambda app_metadata: not app_metadata["is_installed"])

        system_repo_info = system_apps.get_system_repo_info(db.get_data_dir())

        return render_template(
            "manager/addapp.html",
            device=g.user["devices"][device_id],
            apps_list=apps_list,
            custom_apps_list=custom_apps_list,
            system_repo_info=system_repo_info,
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

        app: App = App(
            name=name,
            iname=iname,
            enabled=False,  # start out false, only set to true after configure is finished
            last_render=0,
        )
        user = g.user
        app_details = db.get_app_details_by_name(user["username"], name)
        app_path = app_details.get("path")
        # Override form default with manifest set interval (allow 0 as valid value)
        if "recommended_interval" in app_details:
            uinterval = str(app_details["recommended_interval"])
        if app_path:
            app["path"] = app_path
        if uinterval is not None and uinterval != "":
            app["uinterval"] = int(uinterval)  # convert to int
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


@bp.get("/<string:device_id>/<string:iname>/toggle_pin")
@login_required
def toggle_pin(device_id: str, iname: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")
    user = g.user
    device = user["devices"][device_id]

    if iname not in device.get("apps", {}):
        abort(HTTPStatus.NOT_FOUND, description="App not found")

    # Check if this app is currently pinned
    if device.get("pinned_app") == iname:
        # Unpin it
        device.pop("pinned_app", None)
        flash("App unpinned.")
    else:
        # Pin it
        device["pinned_app"] = iname
        flash("App pinned.")

    db.save_user(user)
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
            if uinterval is not None:
                app["uinterval"] = int(uinterval)
            app["display_time"] = int(request.form.get("display_time", 0))
            if notes:
                app["notes"] = notes
            if start_time:
                app["start_time"] = start_time
            if end_time:
                app["end_time"] = end_time
            app["days"] = request.form.getlist("days")

            # Handle custom recurrence toggle
            use_custom_recurrence = "use_custom_recurrence" in request.form
            app["use_custom_recurrence"] = use_custom_recurrence

            if use_custom_recurrence:
                # Handle new recurrence fields only if custom recurrence is enabled
                recurrence_type = request.form.get("recurrence_type")
                if recurrence_type:
                    app["recurrence_type"] = recurrence_type

                    recurrence_interval = request.form.get("recurrence_interval")
                    if recurrence_interval:
                        try:
                            app["recurrence_interval"] = int(recurrence_interval)
                        except ValueError:
                            pass  # Or flash an error to the user

                    recurrence_start_date = request.form.get("recurrence_start_date")
                    if recurrence_start_date:
                        app["recurrence_start_date"] = recurrence_start_date

                    recurrence_end_date = request.form.get("recurrence_end_date")
                    if recurrence_end_date:
                        app["recurrence_end_date"] = recurrence_end_date
                    else:
                        app.pop("recurrence_end_date", None)  # Remove if empty

                    # Handle recurrence pattern based on type
                    if recurrence_type == "weekly":
                        weekdays = request.form.getlist("weekdays")
                        if weekdays:
                            app["recurrence_pattern"] = {"weekdays": weekdays}
                        else:
                            # Default to all days if none selected
                            app["recurrence_pattern"] = {
                                "weekdays": [
                                    "monday",
                                    "tuesday",
                                    "wednesday",
                                    "thursday",
                                    "friday",
                                    "saturday",
                                    "sunday",
                                ]
                            }

                    elif recurrence_type == "monthly":
                        monthly_pattern = request.form.get("monthly_pattern")
                        if monthly_pattern == "day_of_month":
                            day_of_month = request.form.get("day_of_month")
                            if day_of_month:
                                try:
                                    app["recurrence_pattern"] = {
                                        "day_of_month": int(day_of_month)
                                    }
                                except ValueError:
                                    pass  # Or flash an error to the user
                        elif monthly_pattern == "day_of_week":
                            day_of_week_pattern = request.form.get(
                                "day_of_week_pattern"
                            )
                            if day_of_week_pattern:
                                app["recurrence_pattern"] = {
                                    "day_of_week": day_of_week_pattern
                                }

                    elif recurrence_type == "daily":
                        # Daily doesn't need a specific pattern
                        app.pop("recurrence_pattern", None)

                    elif recurrence_type == "yearly":
                        # For yearly, we can use the start date pattern by default
                        app.pop("recurrence_pattern", None)
            else:
                # Clear custom recurrence fields if not using custom recurrence
                app.pop("recurrence_type", None)
                app.pop("recurrence_interval", None)
                app.pop("recurrence_pattern", None)
                app.pop("recurrence_start_date", None)
                app.pop("recurrence_end_date", None)

            app["enabled"] = enabled
            db.save_user(user)

            return redirect(url_for("manager.index"))
    app = g.user["devices"][device_id]["apps"][iname]

    # Set default dates if not already set
    today = date.today()
    if not app.get("recurrence_start_date"):
        app["recurrence_start_date"] = today.strftime("%Y-%m-%d")
    if not app.get("recurrence_end_date"):
        app["recurrence_end_date"] = (today + timedelta(days=7)).strftime("%Y-%m-%d")

    device = g.user["devices"][device_id]

    return render_template(
        "manager/updateapp.html",
        app=app,
        device=device,
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
        result = call_handler(
            Path(app["path"]), handler, data["param"], current_app.logger
        )

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
            user = db.get_user_by_device_id(device["id"])
            if user:
                app_details = db.get_app_details_by_id(user["username"], app["id"])
                if app_details.get("supports2x", False):
                    magnify = 1
                    width = 128
                    height = 64

    device_interval = device.get("default_interval", 15)
    app_interval = (app and app.get("display_time")) or device_interval

    data, messages = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=width,
        height=height,
        magnify=magnify,
        maxDuration=app_interval * 1000,
        timeout=30000,
        image_format=0,  # 0 == WebP
        logger=current_app.logger,
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
        # current_app.logger.debug(f"{app_path}: {messages}")
        pass

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
        current_app.logger.info(f"{app_basename} -- RENDERING")
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
        # we save app here in case empty_last_render is set, it needs saving and next app won't have a chance to save it
        db.save_app(device_id, app)

        return image is not None  # return false if error

    current_app.logger.info(f"{app_basename} -- NO RENDER")
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
                firmware_data = firmware_utils.generate_firmware(
                    url=image_url,
                    ap=ap,
                    pw=password,
                    device_type=device_type,
                    swap_colors=swap_colors,
                    logger=current_app.logger,
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

    firmware_version = db.get_firmware_version()
    return render_template(
        "manager/firmware.html", device=device, firmware_version=firmware_version
    )


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
        schema_json = get_schema(app_path, current_app.logger)
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
            # we use save_app instead of save_user because a possibly long render just occurred and we don't want to save stale user data.
            db.save_app(device_id, app)
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
    current_app.logger.debug("\n\nStart of next_app")
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    user = db.get_user_by_device_id(device_id) or abort(HTTPStatus.NOT_FOUND)
    device = user["devices"][device_id] or abort(HTTPStatus.NOT_FOUND)

    # first check for a pushed file starting with __ and just return that and then delete it.
    # This needs to happen before brightness check to clear any ephemeral images
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

    # If brightness is 0, short-circuit and return default image to save processing
    brightness = device.get("brightness", 50)
    if brightness == 0:
        current_app.logger.debug("Brightness is 0, returning default image")
        return send_default_image(device)

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

    # Check for pinned app first - this short-circuits all other app selection logic
    pinned_app_iname = device.get("pinned_app")
    is_pinned_app = False
    if pinned_app_iname and pinned_app_iname in apps:
        current_app.logger.debug(f"Using pinned app: {pinned_app_iname}")
        app = apps[pinned_app_iname]
        is_pinned_app = True
        # For pinned apps, we don't update last_app_index since we're not cycling
    else:
        # Normal app selection logic
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

    # For pinned apps, always display them regardless of enabled/schedule status
    # For other apps, check if they should be displayed
    if (
        not is_pinned_app
        and not is_night_mode_app
        and (not app["enabled"] or not db.get_is_app_schedule_active(app, device))
    ):
        # recurse until we find one that's enabled
        current_app.logger.debug(f"{app['name']}-{app['iname']} is disabled")
        return next_app(device_id, last_app_index, recursion_depth + 1)

    # render if necessary, returns false on failure, true for all else
    was_rendered = possibly_render(user, device_id, app)
    if not was_rendered or app.get("empty_last_render", False):
        # try the next app if rendering failed or produced an empty result (no screens)
        return next_app(device_id, last_app_index, recursion_depth + 1)

    if app.get("pushed", False):
        webp_path = (
            db.get_device_webp_dir(device_id) / "pushed" / f"{app['iname']}.webp"
        )
    else:
        app_basename = "{}-{}".format(app["name"], app["iname"])
        webp_path = db.get_device_webp_dir(device_id) / f"{app_basename}.webp"
    current_app.logger.debug(str(webp_path))

    if webp_path.exists() and webp_path.stat().st_size > 0:
        # Get brightness info to combine with app index log and pass to send_image
        b = db.get_device_brightness_8bit(device)
        device_interval = device.get("default_interval", 5)
        s = app.get("display_time", device_interval) if app else device_interval
        if s == 0:
            s = device_interval
        response = send_image(webp_path, device, app, brightness=b, dwell_secs=s)
        immediate = response.headers.get("Tronbyt-Immediate") == "1"
        immediate_text = " -- immediate" if immediate else ""
        current_app.logger.debug(
            f"index {last_app_index} -- bright {b} -- dwell_secs {s}{immediate_text}"
        )
        db.save_last_app_index(device_id, last_app_index)
        return response

    current_app.logger.error(f"file {webp_path} not found")
    # run it recursively until we get a file.
    return next_app(device_id, last_app_index, recursion_depth + 1)


def send_default_image(device: Device) -> ResponseReturnValue:
    return send_image(Path("static/images/default.webp"), device, None)


def send_image(
    webp_path: Path,
    device: Device,
    app: Optional[App],
    immediate: bool = False,
    brightness: Optional[int] = None,
    dwell_secs: Optional[int] = None,
) -> Response:
    if immediate:
        with webp_path.open("rb") as f:
            response = send_file(BytesIO(f.read()), mimetype="image/webp")
    else:
        response = send_file(webp_path, mimetype="image/webp")

    # Use provided brightness or calculate it
    b = brightness if brightness is not None else db.get_device_brightness_8bit(device)

    # Use provided dwell_secs or calculate it
    if dwell_secs is not None:
        s = dwell_secs
    else:
        device_interval = device.get("default_interval", 5)
        s = app.get("display_time", device_interval) if app else device_interval
        if s == 0:
            s = device_interval

    response.headers["Tronbyt-Brightness"] = b
    response.headers["Tronbyt-Dwell-Secs"] = s
    if immediate:
        response.headers["Tronbyt-Immediate"] = "1"
    # Brightness logging moved to next_app function to combine with app index
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
        if set_repo("system_repo_url", db.get_data_dir() / "system-apps", repo_url):
            # run the generate app list for custom repo
            # will just generate json file if already there.
            system_apps.update_system_repo(db.get_data_dir(), current_app.logger)
            return redirect(url_for("manager.index"))
        return redirect(url_for("auth.edit"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/refresh_system_repo", methods=["GET", "POST"])
@login_required
def refresh_system_repo() -> ResponseReturnValue:
    if request.method == "POST":
        if g.user["username"] != "admin":
            abort(HTTPStatus.FORBIDDEN)
        # Directly update the system repo - it handles git pull internally
        system_apps.update_system_repo(db.get_data_dir(), current_app.logger)
        flash("System repo updated successfully")
        return redirect(url_for("manager.index"))
    abort(HTTPStatus.NOT_FOUND)


@bp.route("/mark_app_broken/<string:app_name>", methods=["POST"])
@login_required
def mark_app_broken(app_name: str) -> ResponseReturnValue:
    """Mark an app as broken by adding it to broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if current_app.config.get("PRODUCTION") != "0":
        return {"success": False, "message": "Only available in development mode"}, 403

    # Only allow admin users
    if g.user["username"] != "admin":
        return {"success": False, "message": "Admin access required"}, 403

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
            return {"success": False, "message": "App is already marked as broken"}, 400

        # Add to the list
        broken_apps.append(app_filename)

        # Write back to file
        broken_apps_path.write_text("\n".join(sorted(broken_apps)) + "\n")

        # Regenerate the apps.json to include the broken flag (without doing git pull)
        system_apps.generate_apps_json(db.get_data_dir(), current_app.logger)

        current_app.logger.info(f"Marked {app_filename} as broken")
        return {
            "success": True,
            "message": f"Added {escape(app_filename)} to broken_apps.txt",
        }, 200

    except Exception as e:
        current_app.logger.error(f"Error marking app as broken: {e}")
        return {"success": False, "message": str(e)}, 500


@bp.route("/unmark_app_broken/<string:app_name>", methods=["POST"])
@login_required
def unmark_app_broken(app_name: str) -> ResponseReturnValue:
    """Remove an app from broken_apps.txt (development mode only)."""
    # Only allow in development mode
    if current_app.config.get("PRODUCTION") != "0":
        return {"success": False, "message": "Only available in development mode"}, 403

    # Only allow admin users
    if g.user["username"] != "admin":
        return {"success": False, "message": "Admin access required"}, 403

    try:
        # Get the broken_apps.txt path
        broken_apps_path = db.get_data_dir() / "system-apps" / "broken_apps.txt"

        # Read existing broken apps
        if not broken_apps_path.exists():
            return {"success": False, "message": "No broken apps file found"}, 404

        broken_apps = broken_apps_path.read_text().splitlines()

        # Add .star extension if not present
        app_filename = app_name if app_name.endswith(".star") else f"{app_name}.star"

        # Check if in the list
        if app_filename not in broken_apps:
            return {"success": False, "message": "App is not marked as broken"}, 400

        # Remove from the list
        broken_apps.remove(app_filename)

        # Write back to file
        if broken_apps:
            broken_apps_path.write_text("\n".join(sorted(broken_apps)) + "\n")
        else:
            # If no broken apps left, write empty file
            broken_apps_path.write_text("")

        # Regenerate the apps.json to remove the broken flag (without doing git pull)
        system_apps.generate_apps_json(db.get_data_dir(), current_app.logger)

        current_app.logger.info(f"Unmarked {app_filename} as broken")
        return {
            "success": True,
            "message": f"Removed {escape(app_filename)} from broken_apps.txt",
        }, 200

    except Exception as e:
        current_app.logger.error(f"Error unmarking app: {e}")
        return {"success": False, "message": str(e)}, 500


@bp.route("/update_firmware", methods=["POST"])
@login_required
def update_firmware() -> ResponseReturnValue:
    current_app.logger.info(f"Firmware update requested by user: {g.user['username']}")

    if g.user["username"] != "admin":
        current_app.logger.warning(
            f"Non-admin user {g.user['username']} attempted firmware update"
        )
        abort(HTTPStatus.FORBIDDEN)

    try:
        current_app.logger.info("Starting firmware update...")
        result = firmware_utils.update_firmware_binaries_subprocess(
            db.get_data_dir(), current_app.logger
        )
        current_app.logger.info(f"Firmware update result: {result}")

        if result["success"]:
            if result["action"] == "updated":
                flash(f"✅ {result['message']}", "success")
            elif result["action"] == "skipped":
                flash(f"ℹ️ {result['message']}", "info")
        else:
            flash(f"❌ {result['message']}", "error")

    except Exception as e:
        current_app.logger.error(f"Error updating firmware: {e}")
        flash(f"❌ Firmware update failed: {str(e)}", "error")

    # Redirect back to the edit page with firmware management anchor
    return redirect(url_for("auth.edit") + "#firmware-management")


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


@bp.route("/export_user_config", methods=["GET"])
@login_required
def export_user_config() -> ResponseReturnValue:
    user = g.user.copy()  # Create a copy to avoid modifying the original

    # Remove sensitive data
    user.pop("password", None)

    # Convert the user dictionary to JSON
    user_json = json.dumps(user, indent=4)

    # Create a response to serve the JSON as a file download
    response = Response(
        user_json,
        mimetype="application/json",
        headers={
            "Content-Disposition": f"attachment;filename={user['username']}_config.json"
        },
    )
    return response


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


@bp.route("/import_user_config", methods=["POST"])
@login_required
def import_user_config() -> ResponseReturnValue:
    # Check if the POST request has the file part
    if "file" not in request.files:
        flash("No file part")
        return redirect(url_for("auth.edit"))

    file = request.files["file"]

    # If no file is selected
    if not file.filename:
        flash("No selected file")
        return redirect(url_for("auth.edit"))

    # Ensure the uploaded file is a JSON file
    if not file.filename.endswith(".json"):
        flash("Invalid file type. Please upload a JSON file.")
        return redirect(url_for("auth.edit"))

    # Limit file size to prevent DoS attacks (e.g., 1MB)
    MAX_CONFIG_SIZE = 1 * 1024 * 1024
    file.seek(0, os.SEEK_END)
    if file.tell() > MAX_CONFIG_SIZE:
        flash(f"File size exceeds the limit of {MAX_CONFIG_SIZE // 1024 // 1024}MB.")
        return redirect(url_for("auth.edit"))
    file.seek(0)  # Reset file pointer after checking size

    try:
        # Parse the JSON file
        user_config = json.load(file)

        # Validate the JSON structure (basic validation)
        if not isinstance(user_config, dict):
            flash("Invalid JSON structure")
            return redirect(url_for("auth.edit"))

        # Get the current user
        current_user = g.user

        # Preserve username and password
        username = current_user["username"]
        password = current_user["password"]

        # Check for device ID conflicts and abort if found
        if "devices" in user_config:
            if not isinstance(user_config.get("devices"), dict):
                flash("Invalid JSON structure: 'devices' must be a dictionary.")
                return redirect(url_for("auth.edit"))

            # Validate that device keys match their corresponding "id" entries
            for device_key, device_data in user_config["devices"].items():
                if "id" not in device_data or device_key != device_data["id"]:
                    flash("Corrupted data. Import aborted.")
                    return redirect(url_for("auth.edit"))

            # Get all existing device IDs from other users
            existing_device_ids = set()
            all_users = db.get_all_users()

            for user in all_users:
                if user["username"] != username:  # Skip the current user
                    for device_id in user.get("devices", {}):
                        existing_device_ids.add(device_id)

            # Check each device in the imported config
            for device_id in user_config["devices"].keys():
                # If the device ID already exists for another user, abort the operation
                if device_id in existing_device_ids:
                    flash("Conflicting data. Import aborted.")
                    return redirect(url_for("auth.edit"))

        # Update the current user with the imported data
        current_user.clear()
        current_user.update(user_config)

        # Restore username and password
        current_user["username"] = username
        current_user["password"] = password

        # Save the updated user
        if db.save_user(current_user):
            flash("User configuration imported successfully")
        else:
            flash("Failed to save user configuration")

        return redirect(url_for("auth.edit"))

    except json.JSONDecodeError as e:
        flash(f"Error parsing JSON file: {e}")
        return redirect(url_for("auth.edit"))
    except Exception as e:
        flash(f"Error importing user configuration: {e}")
        return redirect(url_for("auth.edit"))


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

    dwell_time = device.get("default_interval", 5)
    last_brightness = None

    # Track if we've detected old firmware (no "displaying" messages)
    old_firmware_detected = False

    waiter = get_sync_manager().get_waiter(device_id)
    try:
        # Send the first image immediately
        try:
            response = next_app(device_id)
        except Exception as e:
            current_app.logger.error(f"Error in next_app: {e}")
            return

        while ws.connected:
            if isinstance(response, Response):
                if response.status_code == 200:
                    response.direct_passthrough = False  # Disable passthrough mode

                    # Check if this should be displayed immediately (interrupting current display)
                    immediate = response.headers.get("Tronbyt-Immediate")

                    # Get the dwell time from the response header
                    dwell_secs = response.headers.get("Tronbyt-Dwell-Secs")
                    if dwell_secs:
                        dwell_time = int(dwell_secs)

                    # Send dwell_secs as a message to the firmware
                    current_app.logger.debug(
                        f"Sending dwell_secs to device: {dwell_time}s"
                    )
                    ws.send(
                        json.dumps(
                            {
                                "dwell_secs": dwell_time,
                            }
                        )
                    )

                    # Update confirmation timeout now that we have the actual dwell time
                    confirmation_timeout = dwell_time

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

                    # Send metadata message before the image if we need immediate display
                    if immediate:
                        current_app.logger.debug(
                            "Sending immediate display flag to device"
                        )
                        ws.send(
                            json.dumps(
                                {
                                    "immediate": True,
                                }
                            )
                        )

                    # Send the image as a binary message
                    image_data = bytes(response.get_data())
                    current_app.logger.debug(
                        f"Sending image data to device ({len(image_data)} bytes)"
                    )
                    ws.send(image_data)
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

            # Wait for the device to confirm it's displaying the image
            # The device will buffer the image and send this when it starts displaying
            # We check periodically for ephemeral pushes that need immediate sending
            # confirmation_timeout will be set after we get the actual dwell_time from response headers

            # Poll for messages with shorter timeout to allow checking for ephemeral pushes
            poll_interval = 1  # Check every second
            time_waited = 0
            displaying_received = False

            # Use different timeouts based on whether we've detected old firmware
            if old_firmware_detected:
                # Old firmware doesn't send queued/displaying messages, just wait for dwell_time
                extended_timeout = confirmation_timeout
                current_app.logger.debug(
                    f"Using old firmware timeout of {extended_timeout}s (dwell_time)"
                )
            else:
                # New firmware with queued messages - give device full dwell time + buffer
                # Use 2x dwell time to give plenty of room for the device to display current image
                extended_timeout = max(25, int(confirmation_timeout * 2))

            while time_waited < extended_timeout and not displaying_received:
                # First check if there's an ephemeral push waiting
                pushed_dir = db.get_device_webp_dir(device_id) / "pushed"
                if pushed_dir.is_dir() and any(pushed_dir.glob("__*")):
                    current_app.logger.debug(
                        f"[{device_id}] Ephemeral push detected, interrupting wait to send immediately"
                    )
                    # Render the next app (which will pick up the ephemeral push)
                    try:
                        response = next_app(device_id)
                        current_app.logger.debug(
                            f"[{device_id}] Ephemeral push rendered, will send immediately"
                        )
                        break  # Exit the wait loop to send the ephemeral push
                    except Exception as e:
                        current_app.logger.error(f"Error rendering ephemeral push: {e}")
                        continue

                # Wait for a message from the device with short timeout
                try:
                    message = ws.receive(timeout=poll_interval)
                    if message:
                        try:
                            msg_data = json.loads(message)

                            if "queued" in msg_data:
                                # Device has queued/buffered the image: {"queued": counter}
                                # This confirms new firmware - reset old firmware flag and extend timeout
                                queued_counter = msg_data.get("queued")

                                # Reset old firmware detection since device is clearly sending queued messages
                                if old_firmware_detected:
                                    current_app.logger.debug(
                                        "Received 'queued' message - device is actually new firmware, resetting detection"
                                    )
                                    old_firmware_detected = False

                                # Extend timeout since we know it's new firmware and device needs dwell time
                                extended_timeout = max(
                                    extended_timeout, max(25, confirmation_timeout * 2)
                                )
                                current_app.logger.debug(
                                    f"Image queued (seq: {queued_counter}) - using {extended_timeout}s timeout"
                                )

                            elif (
                                "displaying" in msg_data
                                or msg_data.get("status") == "displaying"
                            ):
                                # Device has started displaying the image: {"displaying": counter} or {"status": "displaying", "counter": X}
                                displaying_received = True
                                display_seq = msg_data.get(
                                    "displaying"
                                ) or msg_data.get("counter")
                                current_app.logger.debug(
                                    f"Image displaying (seq: {display_seq})"
                                )

                                # Now render and send the next app immediately
                                try:
                                    response = next_app(device_id)
                                except Exception as e:
                                    current_app.logger.error(
                                        f"Error rendering next app: {e}"
                                    )
                                    continue
                            else:
                                # Unknown message from device, ignore it and keep waiting
                                current_app.logger.warning(
                                    f"[{device_id}] Unknown message format: {message}, ignoring"
                                )
                        except (json.JSONDecodeError, ValueError) as e:
                            # Invalid JSON from device, ignore it and keep waiting
                            current_app.logger.warning(
                                f"Failed to parse device message: {e}"
                            )

                    # Always increment time_waited after each poll, regardless of whether we got a message
                    time_waited += poll_interval
                except Exception:
                    # Timeout on this poll interval, continue waiting
                    time_waited += poll_interval

            # Handle timeout with extended timeout period
            if not displaying_received and time_waited >= extended_timeout:
                if not old_firmware_detected:
                    current_app.logger.info(
                        f"[{device_id}] No 'displaying' message after {extended_timeout}s - marking as old firmware"
                    )
                    old_firmware_detected = True
                current_app.logger.debug(
                    f"No display confirmation received after {extended_timeout}s, assuming old firmware"
                )
                try:
                    response = next_app(device_id)
                except Exception as e:
                    current_app.logger.error(f"Error in next_app: {e}")
                    continue
    except Exception as e:
        current_app.logger.error(f"WebSocket error: {e}")
        ws.close()
    finally:
        current_app.logger.debug(f"WebSocket connection closed (PID: {os.getpid()})")
        waiter.close()


def push_brightness_test_image(device_id: str) -> None:
    """Push an ephemeral brightness test image to the device using test_pattern.webp."""
    try:
        # Use the default.webp from static directory as the test image
        static_dir = Path(current_app.root_path) / "static" / "images"
        default_webp_path = static_dir / "test_pattern.webp"

        if not default_webp_path.exists():
            current_app.logger.warning(
                f"Default test image not found at {default_webp_path}"
            )
            return

        # Read the default image
        webp_bytes = default_webp_path.read_bytes()

        # Save as ephemeral file (starts with __)
        device_webp_path = db.get_device_webp_dir(device_id)
        device_webp_path.mkdir(parents=True, exist_ok=True)
        pushed_path = device_webp_path / "pushed"
        pushed_path.mkdir(exist_ok=True)

        # Use timestamp for unique filename
        filename = f"__brightness_test_{time.monotonic_ns()}.webp"
        file_path = pushed_path / filename

        # Save the image
        file_path.write_bytes(webp_bytes)

        # Notify the device to pick up the new image
        push_new_image(device_id)

        current_app.logger.debug(f"Pushed brightness test image to {device_id}")

    except Exception as e:
        current_app.logger.error(f"Failed to push brightness test image: {e}")
        raise


def push_new_image(device_id: str) -> None:
    """Wake up WebSocket loops to push a new image to a given device."""
    get_sync_manager().notify(device_id)
