import base64
import json
import os
import re
import time
from typing import Any, Dict

from flask import (
    Blueprint,
    Response,
    abort,
    request,
)

# from tronbyt_server.auth import login_required
import tronbyt_server.db as db

bp = Blueprint("api", __name__, url_prefix="/v0")


def get_api_key_from_headers(headers):
    auth_header = headers.get("Authorization")
    if auth_header:
        if auth_header.startswith("Bearer "):
            return auth_header.split(" ")[1]
        else:
            return auth_header
    return None


@bp.route("/devices/<string:device_id>", methods=["GET", "PATCH"])
def get_device(device_id):
    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(400, description="Missing or invalid Authorization header")
    print(f"api_key : {api_key}")
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(404)

    if request.method == "PATCH":
        user = db.get_user_by_device_id(device_id)
        data = request.get_json()
        if "brightness" in data:
            brightness = data["brightness"]
            if brightness < 0 or brightness > 255:
                abort(400, description="Brightness must be between 0 and 255")
            device["brightness"] = brightness
        if "autoDim" in data:
            device["night_mode_enabled"] = data["autoDim"]
        user["devices"][device_id] = device
        db.save_user(user)
    metadata = {
        "id": device["id"],
        "displayName": device["name"],
        "brightness": device["brightness"],
        "autoDim": device["night_mode_enabled"],
    }
    return json.dumps(metadata), 200


@bp.route("/devices/<string:device_id>/push", methods=["POST"])
def handle_push(device_id: str) -> Response:
    # Print out the whole request
    print("Headers:", request.headers)
    # print("JSON Data:", request.get_json())
    print("Body:", request.get_data(as_text=True))

    # get api_key from Authorization header
    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(400, description="Missing or invalid Authorization header")
    print(f"api_key : {api_key}")
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(404)

    # get parameters from JSON data
    try:
        data: Dict[str, Any] = json.loads(request.get_data(as_text=True))
    except json.JSONDecodeError:
        abort(400, description="Invalid JSON data")
    # data = request.get_json()
    print(data)
    installation_id = data.get(
        "installationID", data.get("installationId", "__")
    )  # get both cases ID and Id
    print(f"installation_id:{installation_id}")
    image_data = data.get("image")

    if not api_key or not image_data:
        abort(400, description="Missing required parameters")

    # sanitize installation_id
    installation_id = re.sub(r"[^a-zA-Z0-9_-]", "", installation_id)

    try:
        image_bytes = base64.b64decode(image_data)
    except Exception as e:
        print(str(e))
        abort(400, description="Invalid image data")

    device_webp_path = db.get_device_webp_dir(device_id)
    os.makedirs(device_webp_path, exist_ok=True)
    pushed_path = f"{device_webp_path}/pushed"
    os.makedirs(pushed_path, exist_ok=True)

    # Generate a unique filename using the sanitized installation_id and current timestamp
    timestamp = ""
    if installation_id == "__":
        timestamp = f"{int(time.time())}"
    filename = f"{installation_id}{timestamp}.webp"
    file_path = os.path.join(pushed_path, filename)

    # Save the decoded image data to a file
    with open(file_path, "wb") as f:
        f.write(image_bytes)

    if timestamp == "":
        db.add_pushed_app(
            device_id, file_path
        )  # add the app to user.json so it'll stay in the rotation

    return "Webp received.", 200


########################################################################################################
@bp.route(
    "/devices/<string:device_id>/installations/<string:installation_id>",
    methods=["DELETE"],
)
def handle_delete(device_id: str, installation_id: str) -> Response:
    # get api_key from Authorization header
    api_key = ""
    auth_header = request.headers.get("Authorization")
    if auth_header:
        if auth_header.startswith("Bearer "):
            api_key = auth_header.split(" ")[1]
        else:
            api_key = auth_header
    else:
        abort(400, description="Missing or invalid Authorization header")
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(404)

    if not api_key:
        abort(400, description="Missing required parameters")

    device = db.get_device_by_id(device_id) or abort(404)
    pushed_webp_path = f"{db.get_device_webp_dir(device['id'])}/pushed"
    if not os.path.isdir(pushed_webp_path):
        abort(404, description="Device directory not found")

    # Sanitize installation_id to prevent path traversal attacks
    installation_id = re.sub(r"[^a-zA-Z0-9_-]", "", installation_id)

    # Generate the filename using the installation_id
    file_path = os.path.join(pushed_webp_path, f"{installation_id}.webp")
    print(file_path)
    if not os.path.isfile(file_path):
        abort(404, description="File not found")

    # Delete the file
    os.remove(file_path)

    return "Webp deleted.", 200
