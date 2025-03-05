import base64
import json
import re
import time
from http import HTTPStatus
from typing import Any, Dict, Optional

from flask import (
    Blueprint,
    Response,
    abort,
    current_app,
    request,
)
from flask.typing import ResponseReturnValue
from werkzeug.datastructures import Headers

import tronbyt_server.db as db
from tronbyt_server.models.device import validate_device_id

bp = Blueprint("api", __name__, url_prefix="/v0")


def get_api_key_from_headers(headers: Headers) -> Optional[str]:
    auth_header = headers.get("Authorization")
    if auth_header:
        if auth_header.startswith("Bearer "):
            return auth_header.split(" ")[1]
        else:
            return auth_header
    return None


@bp.route("/devices/<string:device_id>", methods=["GET", "PATCH"])
def get_device(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(
            HTTPStatus.BAD_REQUEST,
            description="Missing or invalid Authorization header",
        )
    current_app.logger.debug(f"api_key : {api_key}")
    user = db.get_user_by_device_id(device_id)
    if not user:
        abort(HTTPStatus.NOT_FOUND)
    device = user["devices"].get(device_id)
    if not device or device["api_key"] != api_key:
        abort(HTTPStatus.NOT_FOUND)

    if request.method == "PATCH":
        data = request.get_json()
        if "brightness" in data:
            brightness = data["brightness"]
            if brightness < 0 or brightness > 255:
                abort(
                    HTTPStatus.BAD_REQUEST,
                    description="Brightness must be between 0 and 255",
                )
            device["brightness"] = brightness
        if "autoDim" in data:
            device["night_mode_enabled"] = data["autoDim"]
        db.save_user(user)
    metadata = {
        "id": device["id"],
        "displayName": device["name"],
        "brightness": device["brightness"],
        "autoDim": device["night_mode_enabled"],
    }
    return json.dumps(metadata), 200


@bp.post("/devices/<string:device_id>/push")
def handle_push(device_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    # Print out the whole request
    current_app.logger.debug("Headers:", request.headers)
    # current_app.logger.debug("JSON Data:", request.get_json())
    current_app.logger.debug("Body:", request.get_data(as_text=True))

    # get api_key from Authorization header
    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(
            HTTPStatus.BAD_REQUEST,
            description="Missing or invalid Authorization header",
        )
    current_app.logger.debug(f"api_key : {api_key}")
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(HTTPStatus.NOT_FOUND)

    # get parameters from JSON data
    try:
        data: Dict[str, Any] = json.loads(request.get_data(as_text=True))
    except json.JSONDecodeError:
        abort(HTTPStatus.BAD_REQUEST, description="Invalid JSON data")
    # data = request.get_json()
    current_app.logger.debug(data)
    installation_id = data.get(
        "installationID", data.get("installationId", "__")
    )  # get both cases ID and Id
    current_app.logger.debug(f"installation_id:{installation_id}")
    image_data = data.get("image")

    if not api_key or not image_data:
        abort(HTTPStatus.BAD_REQUEST, description="Missing required parameters")

    # sanitize installation_id
    installation_id = re.sub(r"[^a-zA-Z0-9_-]", "", installation_id)

    try:
        image_bytes = base64.b64decode(image_data)
    except Exception as e:
        current_app.logger.error(str(e))
        abort(HTTPStatus.BAD_REQUEST, description="Invalid image data")

    device_webp_path = db.get_device_webp_dir(device_id)
    device_webp_path.mkdir(parents=True, exist_ok=True)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    # Generate a unique filename using the sanitized installation_id and current timestamp
    timestamp = ""
    if installation_id == "__":
        timestamp = f"{int(time.time())}"
    filename = f"{installation_id}{timestamp}.webp"
    file_path = pushed_path / filename

    # Save the decoded image data to a file
    file_path.write_bytes(image_bytes)

    if timestamp == "":
        # add the app so it'll stay in the rotation
        db.add_pushed_app(device_id, file_path)

    return Response("Webp received.", status=200)


########################################################################################################
@bp.route(
    "/devices/<string:device_id>/installations/<string:installation_id>",
    methods=["DELETE"],
)
def handle_delete(device_id: str, installation_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    # get api_key from Authorization header
    api_key = ""
    auth_header = request.headers.get("Authorization")
    if auth_header:
        if auth_header.startswith("Bearer "):
            api_key = auth_header.split(" ")[1]
        else:
            api_key = auth_header
    else:
        abort(
            HTTPStatus.BAD_REQUEST,
            description="Missing or invalid Authorization header",
        )
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(HTTPStatus.NOT_FOUND)

    if not api_key:
        abort(HTTPStatus.BAD_REQUEST, description="Missing required parameters")

    device = db.get_device_by_id(device_id) or abort(HTTPStatus.NOT_FOUND)
    pushed_webp_path = db.get_device_webp_dir(device["id"]) / "pushed"
    if not pushed_webp_path.is_dir():
        abort(HTTPStatus.NOT_FOUND, description="Device directory not found")

    # Sanitize installation_id to prevent path traversal attacks
    installation_id = re.sub(r"[^a-zA-Z0-9_-]", "", installation_id)

    # Generate the filename using the installation_id
    file_path = pushed_webp_path / f"{installation_id}.webp"
    current_app.logger.debug(file_path)
    if not file_path.is_file():
        abort(HTTPStatus.NOT_FOUND, description="File not found")

    # Delete the file
    file_path.unlink()

    return Response("Webp deleted.", status=200)
