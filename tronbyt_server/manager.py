import json
import os
import secrets
import shutil
import string
import subprocess
import time
import uuid
from typing import Any, Dict, Optional

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
from websocket import create_connection

import tronbyt_server.db as db
from tronbyt_server import render_app as pixlet_render_app
from tronbyt_server.auth import login_required

bp = Blueprint("manager", __name__)


@bp.route("/")
@login_required
def index() -> str:
    # os.system("pkill -f serve") # kill any pixlet serve processes
    devices = dict()

    if not g.user:
        print("check [user].json file, might be corrupted")

    if "devices" in g.user:
        devices = reversed(list(g.user["devices"].values()))
    server_root = f"{current_app.config['SERVER_PROTOCOL']}://{current_app.config['SERVER_HOSTNAME']}:{current_app.config['MAIN_PORT']}"
    return render_template(
        "manager/index.html", devices=devices, server_root=server_root
    )


# function to handle uploading a an app
@bp.route("/uploadapp", methods=["GET", "POST"])
@login_required
def uploadapp() -> str:
    user_apps_path = f"{db.get_users_dir()}/{g.user['username']}/apps"
    if request.method == "POST":
        # check if the post request has the file part
        if "file" not in request.files:
            flash("No file part")
            return redirect("manager.uploadapp")
        file = request.files["file"]
        # if user does not select file, browser also
        # submit an empty part without filename
        if file:
            if file.filename == "":
                flash("No file")
                return redirect("manager.uploadapp")

            # save the file to the user's
            if db.save_user_app(file, user_apps_path):
                flash("Upload Successful")
                return redirect(url_for("manager.index"))
            else:
                flash("Save Failed")
                return redirect(url_for("manager.uploadapp"))

    # check for existance of apps path
    os.makedirs(user_apps_path, exist_ok=True)

    star_files = [file for file in os.listdir(user_apps_path) if file.endswith(".star")]

    return render_template("manager/uploadapp.html", files=star_files)


# function to delete an uploaded star file
@bp.route("/deleteupload/<string:filename>", methods=["POST", "GET"])
@login_required
def deleteupload(filename: str) -> Response:
    db.delete_user_upload(g.user, filename)
    return redirect(url_for("manager.uploadapp"))


@bp.route("/adminindex")
@login_required
def adminindex() -> str:
    if g.user["username"] != "admin":
        abort(404)
    userlist = list()
    # go through the users folder and build a list of all users
    users = os.listdir("users")
    # read in the user.config file
    for username in users:
        user = db.get_user(username)
        if user:
            userlist.append(user)

    return render_template("manager/adminindex.html", users=userlist)


@bp.route("/admin/<string:username>/deleteuser", methods=["POST"])
@login_required
def deleteuser(username: str) -> Response:
    if g.user["username"] != "admin":
        abort(404)
    if username != "admin":
        db.delete_user(username)
    return redirect(url_for("manager.adminindex"))


@bp.route("/create", methods=["GET", "POST"])
@login_required
def create() -> str:
    if request.method == "POST":
        name = request.form.get("name")
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
            device = dict()
            # just use first 8 chars is good enough
            device["id"] = str(uuid.uuid4())[0:8]
            print("device_id is :" + str(device["id"]))
            device["name"] = name
            if not img_url:
                img_url = f"{current_app.config['SERVER_PROTOCOL']}://{current_app.config['SERVER_HOSTNAME']}:{current_app.config['MAIN_PORT']}/{device['id']}/next"
            device["img_url"] = img_url
            if not api_key or api_key == "":
                api_key = "".join(
                    secrets.choice(string.ascii_letters + string.digits)
                    for _ in range(32)
                )
            device["api_key"] = api_key
            device["notes"] = notes
            device["brightness"] = int(brightness) if brightness else 30
            user = g.user
            if "devices" not in user:
                user["devices"] = {}

            user["devices"][device["id"]] = device
            if db.save_user(user) and not os.path.isdir(
                db.get_device_webp_dir(device["id"])
            ):
                os.mkdir(db.get_device_webp_dir(device["id"]))

            return redirect(url_for("manager.index"))
    return render_template("manager/create.html")


@bp.route("/<string:device_id>/update_brightness", methods=["GET", "POST"])
@login_required
def update_brightness(device_id: str) -> Response:
    if device_id not in g.user["devices"]:
        abort(404)
    if request.method == "POST":
        brightness = int(request.form.get("brightness"))
        user = g.user
        user["devices"][device_id]["brightness"] = brightness
        db.save_user(user)
        return "", 200


@bp.route("/<string:device_id>/update_interval", methods=["GET", "POST"])
@login_required
def update_interval(device_id: str) -> Response:
    if device_id not in g.user["devices"]:
        abort(404)
    if request.method == "POST":
        interval = int(request.form.get("interval"))
        g.user["devices"][device_id]["default_interval"] = interval
        db.save_user(g.user)
        return "", 200


@bp.route("/<string:device_id>/update", methods=["GET", "POST"])
@login_required
def update(device_id: str) -> str:
    id = device_id
    if id not in g.user["devices"]:
        abort(404)
    if request.method == "POST":
        name = request.form.get("name")
        notes = request.form.get("notes")
        img_url = request.form.get("img_url")
        api_key = request.form.get("api_key")
        error = None
        if not name or not id:
            error = "Id and Name is required."
        if error is not None:
            flash(error)
        else:
            device = dict()
            device["id"] = id
            device["name"] = name
            device["default_interval"] = int(request.form.get("default_interval"))
            device["brightness"] = int(request.form.get("brightness"))
            device["night_brightness"] = int(request.form.get("night_brightness"))
            device["night_start"] = int(request.form.get("night_start"))
            device["timezone"] = int(request.form.get("timezone"))
            device["img_url"] = (
                db.sanitize_url(img_url)
                if len(img_url) > 0
                else f"{current_app.config['SERVER_PROTOCOL']}://{current_app.config['SERVER_HOSTNAME']}:{current_app.config['MAIN_PORT']}/{device['id']}/next"
            )
            device["night_mode_app"] = request.form.get("night_mode_app")
            device["api_key"] = api_key
            device["notes"] = notes

            user = g.user
            if "apps" in user["devices"][id]:
                device["apps"] = user["devices"][id]["apps"]
            user["devices"][id] = device
            db.save_user(user)

            return redirect(url_for("manager.index"))
    device = g.user["devices"][id]
    server_root = f"{current_app.config['SERVER_PROTOCOL']}://{current_app.config['SERVER_HOSTNAME']}:{current_app.config['MAIN_PORT']}"
    return render_template(
        "manager/update.html", device=device, server_root=server_root
    )


@bp.route("/<string:device_id>/delete", methods=["POST"])
@login_required
def delete(device_id: str) -> Response:
    device = g.user["devices"].get(device_id, None)
    if device:
        g.user["devices"].pop(device_id)
        db.save_user(g.user)
        db.delete_device_dirs(device_id)
        return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/<string:iname>/delete", methods=["POST", "GET"])
@login_required
def deleteapp(device_id: str, iname: str) -> Response:
    users_dir = db.get_users_dir()
    config_path = "{}/{}/configs/{}-{}.json".format(
        users_dir,
        g.user["username"],
        g.user["devices"][device_id]["apps"][iname]["name"],
        g.user["devices"][device_id]["apps"][iname]["iname"],
    )
    tmp_config_path = "{}/{}/configs/{}-{}.tmp".format(
        users_dir,
        g.user["username"],
        g.user["devices"][device_id]["apps"][iname]["name"],
        g.user["devices"][device_id]["apps"][iname]["iname"],
    )
    if os.path.isfile(config_path):
        os.remove(config_path)
    if os.path.isfile(tmp_config_path):
        os.remove(tmp_config_path)

    # use pixlet to delete installation of app if api_key exists (device_tidbyt server operation) and enabled flag is set to true
    if (
        "use_tidbyt" in g.user["devices"][device_id]
        and "api_key" in g.user["devices"][device_id]
        and g.user["devices"][device_id]["apps"][iname]["enabled"] == "true"
    ):
        command = [
            "/pixlet/pixlet",
            "delete",
            g.user["devices"][device_id]["img_url"],
            iname,
            "-t",
            g.user["devices"][device_id]["api_key"],
        ]
        print("Deleting installation id {}".format(iname))
        subprocess.run(command)
    device = g.user["devices"][device_id]
    app = g.user["devices"][device_id]["apps"][iname]

    if "pushed" in app:
        webp_path = "{}/pushed/{}.webp".format(
            db.get_device_webp_dir(device["id"]), app["name"]
        )
    else:
        # delete the webp file
        webp_path = "{}/{}-{}.webp".format(
            db.get_device_webp_dir(device["id"]),
            app["name"],
            app["iname"],
        )
    # if file exists remove it
    if os.path.isfile(webp_path):
        os.remove(webp_path)
    # pop the app from the user object
    g.user["devices"][device_id]["apps"].pop(iname)
    db.save_user(g.user)
    return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/addapp", methods=["GET", "POST"])
@login_required
def addapp(device_id: str) -> Response:
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
        app_details = db.get_app_details(g.user["username"], name)
        uinterval = int(request.form.get("uinterval"))
        display_time = int(request.form.get("display_time"))
        notes = request.form.get("notes")
        error = None
        # generate an iname from 3 digits. will be used later as the port number on which to run pixlet serve
        import random

        iname = str(random.randint(100, 999))

        if not name:
            error = "App name required."
        if db.file_exists("configs/{}-{}.json".format(name, iname)):
            error = "That installation id already exists"
        if error is not None:
            flash(error)
        else:
            app = dict()
            app["iname"] = iname
            print("iname is :" + str(app["iname"]))
            app["name"] = name
            app["uinterval"] = uinterval
            app["display_time"] = display_time
            app["notes"] = notes
            app["enabled"] = (
                "false"  # start out false, only set to true after configure is finshed
            )
            app["last_render"] = 0
            if "path" in app_details:
                # this indicates a custom app
                app["path"] = app_details["path"]

            user = g.user
            if "apps" not in user["devices"][device_id]:
                user["devices"][device_id]["apps"] = {}

            user["devices"][device_id]["apps"][iname] = app
            db.save_user(user)

            return redirect(
                url_for(
                    "manager.configapp",
                    device_id=device_id,
                    iname=iname,
                    delete_on_cancel=1,
                )
            )
    else:
        abort(404)


@bp.route("/<string:device_id>/<string:iname>/toggle_enabled", methods=["GET"])
@login_required
def toggle_enabled(device_id: str, iname: str) -> Response:
    user = g.user
    app = user["devices"][device_id]["apps"][iname]

    if user["devices"][device_id]["apps"][iname]["enabled"] == "true":
        app["enabled"] = "false"
        # set fresh_disable so we can delete from tidbyt once and only once
        # use pixlet to delete installation of app if api_key exists (tidbyt server operation) and enabled flag is set to true
        if (
            "use_tidbyt" in g.user["devices"][device_id]
            and "api_key" in g.user["devices"][device_id]
        ):
            command = [
                "/pixlet/pixlet",
                "delete",
                g.user["devices"][device_id]["img_url"],
                iname,
                "-t",
                g.user["devices"][device_id]["api_key"],
            ]
            print(command)
            subprocess.run(command)
            app["deleted"] = "true"
    else:
        # we should probably re-render and push but that'a  a pain so not doing it right now.
        app["enabled"] = "true"

    user["devices"][device_id]["apps"][iname] = app
    db.save_user(user)  # this saves all changes
    flash("Changes saved.")
    return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/<string:iname>/updateapp", methods=["GET", "POST"])
@login_required
def updateapp(device_id: str, iname: str) -> Response:
    if request.method == "POST":
        name = request.form.get("name")
        uinterval = request.form.get("uinterval")
        notes = request.form.get("notes")
        if "enabled" in request.form:
            enabled = "true"
        else:
            enabled = "false"
        print(request.form)
        error = None
        if not name or not iname:
            error = "Name and installation_id is required."
        if error is not None:
            flash(error)
        else:
            user = g.user
            app = user["devices"][device_id]["apps"][iname]
            app["iname"] = iname
            print("iname is :" + str(app["iname"]))
            app["name"] = name
            app["uinterval"] = uinterval
            app["display_time"] = int(request.form.get("display_time")) or 0
            app["notes"] = notes
            app["start_time"] = request.form.get("start_time")
            app["end_time"] = request.form.get("end_time")
            app["days"] = request.form.getlist("days")

            if (
                user["devices"][device_id]["apps"][iname]["enabled"] == "true"
                and enabled == "false"
            ):
                # set fresh_disable so we can delete from tidbyt once and only once
                # use pixlet to delete installation of app if api_key exists (tidbyt server operation) and enabled flag is set to true
                if (
                    "use_tidbyt" in g.user["devices"][device_id]
                    and "api_key" in g.user["devices"][device_id]
                ):
                    command = [
                        "/pixlet/pixlet",
                        "delete",
                        g.user["devices"][device_id]["img_url"],
                        iname,
                        "-t",
                        g.user["devices"][device_id]["api_key"],
                    ]
                    print(command)
                    subprocess.run(command)
                    app["deleted"] = "true"
            app["enabled"] = enabled
            user["devices"][device_id]["apps"][iname] = app
            db.save_user(user)  # this saves all changes

            return redirect(url_for("manager.index"))
    app = g.user["devices"][device_id]["apps"][iname]
    return render_template("manager/updateapp.html", app=app, device_id=device_id)


def render_app(app_path: str, config_path: str, webp_path: str) -> bool:
    config_data = ""
    if os.path.exists(config_path):
        with open(config_path, "r") as config_file:
            config_data = json.load(config_file)
    data = pixlet_render_app(
        app_path,
        config_data,
        64,
        32,
        1,
        15000,
        30000,
        False,
        True,
    )
    if not data:
        print("\t\t\tError running pixlet render")
        return False
    with open(webp_path, "wb") as webp_file:
        webp_file.write(data)
    return True


def possibly_render(user: Dict[str, Any], device_id: str, app: Dict[str, Any]) -> bool:
    result = False
    if "pushed" in app:
        print("Pushed App -- NO RENDER")
        return result
    if not app.get("enabled", True):
        print(f"{app['name']} -- Disabled")
        return result
    now = int(time.time())
    app_basename = "{}-{}".format(app["name"], app["iname"])
    config_path = "users/{}/configs/{}.json".format(user["username"], app_basename)
    webp_device_path = db.get_device_webp_dir(device_id)
    os.makedirs(webp_device_path, exist_ok=True)
    webp_path = "{}/{}.webp".format(webp_device_path, app_basename)

    if "path" in app:
        app_path = app["path"]
    else:
        # print("\t\t\tNo path for {}, trying default location".format(app["name"]))
        app_path = "system-apps/apps/{}/{}.star".format(
            app["name"].replace("_", ""), app["name"]
        )
    if (
        "last_render" not in app
        or now - app["last_render"] > int(app["uinterval"]) * 60
    ):
        print(f"\nRENDERING -- {app_basename}")
        if render_app(app_path, config_path, webp_path):
            # update the config file with the new last render time
            app["last_render"] = int(time.time())
            result = True
    else:
        print(f"\n{app_basename} -- NO RENDER")
    return result


@bp.route("/<string:device_id>/firmware", methods=["POST", "GET"])
@login_required
def generate_firmware(device_id: str) -> Response:
    # first ensure this device id exists in the current users config
    if device_id not in g.user["devices"]:
        abort(404)
    # on GET just render the form for the user to input their wifi creds and auto fill the image_url

    if request.method == "POST":
        print(request.form)
        if "wifi_ap" in request.form and "wifi_password" in request.form:
            ap = request.form.get("wifi_ap")
            password = request.form.get("wifi_password")
            image_url = request.form.get("img_url")
            label = db.sanitize(g.user["devices"][device_id]["name"])
            gen2 = False
            swap_colors = False
            if "gen2" in request.form:
                gen2 = request.form.get("gen2")
            if "swap_colors" in request.form:
                swap_colors = request.form.get("swap_colors")

            result = db.generate_firmware(
                label, image_url, ap, password, gen2, swap_colors
            )
            if "file_path" in result:
                g.user["devices"][device_id]["firmware_file_path"] = result["file_path"]
                db.save_user(g.user)
                return render_template(
                    "manager/firmware.html",
                    device=g.user["devices"][device_id],
                    img_url=image_url,
                    ap=ap,
                    password=password,
                    firmware_file=result["file_path"],
                )
            elif "error" in result:
                flash(result["error"])
            else:
                flash("firmware modification failed")

    return render_template(
        "manager/firmware_form.html",
        device=g.user["devices"][device_id],
        server_root=f"{current_app.config['SERVER_PROTOCOL']}://{current_app.config['SERVER_HOSTNAME']}:{current_app.config['MAIN_PORT']}",
    )


@bp.route(
    "/<string:device_id>/<string:iname>/<int:delete_on_cancel>/configapp",
    methods=["GET", "POST"],
)
@login_required
def configapp(device_id: str, iname: str, delete_on_cancel: int) -> Response:
    users_dir = db.get_users_dir()
    # used when rendering configapp
    domain_host = current_app.config["SERVER_HOSTNAME"]
    protocol = current_app.config["SERVER_PROTOCOL"]

    app = g.user["devices"][device_id]["apps"][iname]
    app_basename = "{}-{}".format(app["name"], app["iname"])
    app_details = db.get_app_details(g.user["username"], app["name"])
    if "path" in app_details:
        app_path = app_details["path"]
    else:
        app_path = "system-apps/apps/{}/{}.star".format(
            app["name"].replace("_", ""), app["name"]
        )
    config_path = "{}/{}/configs/{}.json".format(
        users_dir, g.user["username"], app_basename
    )
    tmp_config_path = "{}/{}/configs/{}.tmp".format(
        users_dir, g.user["username"], app_basename
    )
    webp_device_path = db.get_device_webp_dir(device_id)
    os.makedirs(webp_device_path, exist_ok=True)
    webp_path = "{}/{}.webp".format(webp_device_path, app_basename)

    user_render_port = str(db.get_user_render_port(g.user["username"]))
    # always kill the pixlet proc based on port number.
    os.system(
        "pkill -f {}".format(user_render_port)
    )  # kill pixlet process based on port

    if request.method == "POST":
        #   do something to confirm configuration ?
        print("checking for : " + tmp_config_path)
        if db.file_exists(tmp_config_path):
            print("file exists")
            with open(tmp_config_path, "r") as c:
                new_config = c.read()
            # flash(new_config)
            with open(config_path, "w") as config_file:
                config_file.write(new_config)

            # delete the tmp file
            os.remove(tmp_config_path)

            # run pixlet render with the new config file
            print("rendering")
            device = g.user["devices"][device_id]
            if render_app(app_path, config_path, webp_path):
                # set the enabled key in app to true now that it has been configured.
                device["apps"][iname]["enabled"] = "true"
                # set last_rendered to seconds
                device["apps"][iname]["last_render"] = int(time.time())

                if "use_tidyt" in device and device["api_key"] != "":
                    device = g.user["devices"][device_id]
                    # check for zero filesize
                    if os.path.getsize(webp_path) > 0:
                        command = [
                            "/pixlet/pixlet",
                            "push",
                            device["img_url"],
                            webp_path,
                            "-b",
                            "-t",
                            device["api_key"],
                            "-i",
                            app["iname"],
                        ]

                        print("pushing {}".format(app["iname"]))
                        result = subprocess.run(command)
                        if "deleted" in app:
                            del app["deleted"]
                    else:
                        # delete installation may error if the installation doesn't exist but that's ok.
                        command = [
                            "/pixlet/pixlet",
                            "delete",
                            device["img_url"],
                            app["iname"],
                            "-t",
                            device["api_key"],
                        ]
                        print("blank output, deleting {}".format(app["iname"]))
                        result = subprocess.run(command)
                        app["deleted"] = "true"
                    if result == 0:
                        pass
                    else:
                        print("error pushing App: " + str(result))
                        flash("Error Pushing App")

                # always save
                db.save_user(g.user)
            else:
                flash("Error Rendering App")

        return redirect(url_for("manager.index"))

    # run the in browser configure interface via pixlet serve
    elif request.method == "GET":
        url_params = ""
        if db.file_exists(config_path):
            import json
            import urllib.parse

            with open(config_path, "r") as c:
                config_dict = json.load(c)
            url_params = urllib.parse.urlencode(config_dict)
            print(url_params)
            if len(url_params) > 2:
                flash(url_params)

        # should add the device locaation/timezome info to the url params here
        #

        # ./pixlet serve --saveconfig "noaa_buoy.config" --host 0.0.0.0 src/apps/noaa_buoy.star
        # execute the pixlet serve process and show in it an iframe on the config page.
        print(app_path)
        if db.file_exists(app_path):
            subprocess.Popen(
                [
                    "timeout",
                    "-k",
                    "300",
                    "300",
                    "/pixlet/pixlet",
                    "--saveconfig",
                    tmp_config_path,
                    "serve",
                    app_path,
                    "--port={}".format(user_render_port),
                    "--path=/pixlet/",
                ],
                shell=False,
            )

            # give pixlet some time to start up
            time.sleep(2)
            return render_template(
                "manager/configapp.html",
                app=app,
                domain_host=domain_host,
                protocol=protocol,
                url_params=url_params,
                device_id=device_id,
                delete_on_cancel=delete_on_cancel,
                user_render_port=user_render_port,
            )

        else:
            flash("App Not Found")
            return redirect(url_for("manager.index"))


@bp.route("/<string:device_id>/brightness", methods=["GET"])
def get_brightness(device_id: str) -> Response:
    user = db.get_user_by_device_id(device_id)
    device = list(user["devices"].values())[0]
    brightness_value = device.get("brightness", 30)
    print(f"brightness value {brightness_value}")
    return Response(str(brightness_value), mimetype="text/plain")


MAX_RECURSION_DEPTH = 10


@bp.route("/<string:device_id>/next")
def next_app(
    device_id: str,
    user: Optional[Dict[str, Any]] = None,
    last_app_index: Optional[int] = None,
    recursion_depth: int = 0,
) -> Response:
    user = db.get_user_by_device_id(device_id) or abort(404)
    device = user["devices"][device_id] or abort(404)

    # first check for a pushed file starting with __ and just return that and then delete it.
    pushed_dir = f"{db.get_device_webp_dir(device_id)}/pushed"
    if os.path.isdir(pushed_dir):
        ephemeral_files = [f for f in os.listdir(pushed_dir) if f.startswith("__")]
        if ephemeral_files:
            ephemeral_file = ephemeral_files[0]
            print(f"\nreturning ephemeral pushed file {ephemeral_file}")
            webp_path = f"webp/{device_id}/pushed/{ephemeral_file}"
            # send_file doesn't need the full path, just the path after the tronbyt_server
            response = send_file(webp_path, mimetype="image/webp")
            s = device.get("default_interval", 5)
            response.headers["Tronbyt-Dwell-Secs"] = s
            print("removing ephemeral webp")
            os.remove(f"{pushed_dir}/{ephemeral_file}")
            return response

    if recursion_depth > MAX_RECURSION_DEPTH:
        print("Maximum recursion depth exceeded, sending default webp")
        response = send_file("static/images/default.webp", mimetype="image/webp")
        response.headers["Tronbyt-Brightness"] = 8
        return response

    if not user:
        user = db.get_user_by_device_id(device_id)
    if last_app_index is None:
        last_app_index = db.get_last_app_index(device_id)

    # Pick device by passed in device_id
    device = user["devices"][device_id]

    # treat em like an array
    if "apps" not in device:
        return next_app(device_id, user, 0, recursion_depth + 1)
    apps_list = list(device["apps"].values())
    if (
        db.get_night_mode_is_active(device)
        and device.get("night_mode_app", "") in device["apps"].keys()
    ):
        next_app_dict = device["apps"][device["night_mode_app"]]
    else:
        if last_app_index + 1 < len(apps_list):  # will +1 be in bounds of array ?
            next_app_dict = apps_list[last_app_index + 1]  # add 1 to get the next app
            last_app_index += 1
        else:
            next_app_dict = apps_list[0]  # go to the beginning
            last_app_index = 0

    app = next_app_dict

    if app["enabled"] == "false" or not db.get_is_app_schedule_active(app):
        # recurse until we find one that's enabled
        print("disabled app")
        time.sleep(0.25)  # delay when recursing to avoid accidental runaway
        return next_app(device_id, user, last_app_index + 1, recursion_depth + 1)
    else:
        # check if the webp needs update/render and do it, save if rendered
        if possibly_render(user, device_id, app):
            db.save_user(user)

        if "pushed" in app:
            webp_path = "{}/pushed/{}.webp".format(
                db.get_device_webp_dir(device_id), app["iname"]
            )
        else:
            app_basename = "{}-{}".format(app["name"], app["iname"])
            webp_path = "{}/{}.webp".format(
                db.get_device_webp_dir(device_id), app_basename
            )
        print(webp_path)

        if db.file_exists(webp_path) and os.path.getsize(webp_path) > 0:
            response = send_file(webp_path, mimetype="image/webp")
            b = db.get_device_brightness(device)
            print(f"sending brightness {b} -- ", end="")
            response.headers["Tronbyt-Brightness"] = b
            s = app.get("display_time", None)
            if not s or int(s) == 0:
                s = device.get("default_interval", 5)
            print(f"sending dwell seconds {s} -- ", end="")
            response.headers["Tronbyt-Dwell-Secs"] = s
            print(f"app index is {last_app_index}")
            db.save_last_app_index(device_id, last_app_index)
            return response
        else:
            print("file not found")
            # delay when recursing to avoid accidental runaway
            time.sleep(0.25)
            # run it recursively until we get a file.
            return next_app(device_id, user, last_app_index + 1, recursion_depth + 1)


@bp.route("/<string:device_id>/<string:iname>/appwebp")
def appwebp(device_id: str, iname: str) -> Response:
    try:
        if g.user:
            app = g.user["devices"][device_id]["apps"][iname]
        else:
            app = db.get_user("admin")["devices"][device_id]["apps"][iname]

        app_basename = "{}-{}".format(app["name"], app["iname"])

        if "pushed" in app:
            webp_path = "{}/pushed/{}.webp".format(
                db.get_device_webp_dir(device_id), app["iname"]
            )
        else:
            webp_path = "{}/{}.webp".format(
                db.get_device_webp_dir(device_id), app_basename
            )
        print(webp_path)
        if db.file_exists(webp_path) and os.path.getsize(webp_path) > 0:
            return send_file(webp_path, mimetype="image/webp")
        else:
            print("file no exist or 0 size")
            abort(404)
    except Exception as e:
        print(f"Exception: {str(e)}")
        abort(404)


@bp.route("/<string:device_id>/download_firmware")
@login_required
def download_firmware(device_id: str) -> Response:
    try:
        if (
            g.user
            and device_id in g.user["devices"]
            and "firmware_file_path" in g.user["devices"][device_id]
        ):
            file_path = g.user["devices"][device_id]["firmware_file_path"]
        else:
            abort(404)

        print(f"checking for {file_path}")
        if db.file_exists(file_path) and os.path.getsize(file_path) > 0:
            return send_file(file_path, mimetype="application/octet-stream")
        else:
            print("file no exist or 0 size")
            abort(404)
    except Exception as e:
        print(f"Exception: {str(e)}")
        abort(404)


@bp.route("/set_user_repo", methods=["GET", "POST"])
@login_required
def set_user_repo() -> Response:
    if request.method == "POST":
        if "app_repo_url" in request.form:
            repo_url = request.form.get("app_repo_url")
            print(repo_url)
            user_apps_path = "{}/{}/apps".format(db.get_users_dir(), g.user["username"])
            old_repo = ""
            if "app_repo_url" in g.user:
                old_repo = g.user["app_repo_url"]

            if repo_url != "":
                if old_repo != repo_url:
                    # just get the last two words of the repo
                    repo_url = repo_url.split("/")[-2:]
                    repo_url = "/".join(repo_url)
                    g.user["app_repo_url"] = repo_url
                    db.save_user(g.user)

                    print(user_apps_path)
                    if db.file_exists(user_apps_path):
                        shutil.rmtree(user_apps_path)
                    result = subprocess.run(
                        [
                            "git",
                            "clone",
                            f"https://blah:blah@github.com/{repo_url}",
                            user_apps_path,
                        ]
                    )
                    if result.returncode == 0:
                        flash("Repo Cloned")
                else:
                    # same as before so just issue a pull to update it.
                    result = subprocess.run(["git", "-C", "pull", user_apps_path])
                    if result.returncode == 0:
                        flash("Repo Updated")
                # run the generate app list for custom repo
                return redirect(url_for("manager.index"))

            else:
                flash("No Changes to Repo")

            flash("Error Saving Repo")
        return redirect(url_for("auth.edit"))
    abort(404)


@bp.route("/set_system_repo", methods=["GET", "POST"])
@login_required
def set_system_repo() -> Response:
    if request.method == "POST":
        if g.user["username"] != "admin":
            abort(404)
        if "app_repo_url" in request.form:
            repo_url = request.form.get("app_repo_url")
            print(repo_url)
            system_apps_path = "system-apps"
            old_repo = ""
            if "system_repo_url" in g.user:
                old_repo = g.user["system_repo_url"]

            if repo_url != "":
                if old_repo != repo_url:
                    # just get the last two words of the repo
                    repo_url = repo_url.split("/")[-2:]
                    repo_url = "/".join(repo_url)
                    g.user["system_repo_url"] = repo_url
                    db.save_user(g.user)

                    print(system_apps_path)
                    if db.file_exists(system_apps_path):
                        # delete the folder and re-clone.
                        print(f"deleting {system_apps_path}")
                        shutil.rmtree(system_apps_path)
                    # pull the repo and save to local filesystem.
                    # result = os.system("git clone https://blah:blah@github.com/{} {}".format(repo_url,system_apps_path))
                    result = subprocess.run(
                        [
                            "git",
                            "clone",
                            "--depth",
                            "1",
                            f"https://blah:blah@github.com/{repo_url}",
                            system_apps_path,
                        ]
                    )
                    if result.returncode != 0:
                        flash("Error Cloning Repo")
                    else:
                        flash("Repo Cloned")
                else:
                    # same as before so just issue a pull to update it.
                    result = subprocess.run(["git", "-C", system_apps_path, "pull"])
                    if result.returncode == 0:
                        flash("Repo Updated")
                    else:
                        flash("Repo Update Failed")
                # run the generate app list for custom repo
                # will just generate json file if already there.
                os.system("python3 clone_system_apps_repo.py")
                return redirect(url_for("manager.index"))

            else:
                flash("No Changes to Repo")

            flash("Error Saving Repo")
        return redirect(url_for("auth.edit"))
    abort(404)


@bp.route("/pixlet", defaults={"path": ""}, methods=["GET", "POST"])
@bp.route("/pixlet/<path:path>", methods=["GET", "POST"])
@login_required
def pixlet_proxy(path: str) -> Response:
    user_render_port = db.get_user_render_port(g.user["username"])
    pixlet_url = f"http://localhost:{user_render_port}/pixlet/{path}?{request.query_string.decode()}"
    try:
        if request.method == "GET":
            response = requests.get(
                pixlet_url, params=request.args, headers=request.headers
            )
        elif request.method == "POST":
            response = requests.post(
                pixlet_url, data=request.get_data(), headers=request.headers
            )
        response.raise_for_status()
    except requests.exceptions.RequestException as e:
        print(f"Error fetching {pixlet_url}: {e}")
        abort(500)
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


@bp.route("/pixlet/api/v1/ws/<path:url>")
@login_required
def proxy_ws(url: str) -> Response:
    user_render_port = db.get_user_render_port(g.user["username"])
    pixlet_url = f"ws://localhost:{user_render_port}/pixlet/{url}"
    ws = create_connection(pixlet_url)
    ws.send(request.data)
    response = ws.recv()
    ws.close()
    return Response(response, content_type="application/json")
