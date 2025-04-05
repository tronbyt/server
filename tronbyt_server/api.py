import base64
import json
import time
from http import HTTPStatus
from pathlib import Path
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
from werkzeug.utils import secure_filename

import tronbyt_server.db as db
import tronbyt_server.manager as manager
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
            brightness = int(data["brightness"])
            if brightness < 0 or brightness > 255:
                abort(
                    HTTPStatus.BAD_REQUEST,
                    description="Brightness must be between 0 and 255",
                )
            device["brightness"] = db.brightness_map_8bit_to_levels(brightness)
        if "autoDim" in data:
            device["night_mode_enabled"] = bool(data["autoDim"])
        db.save_user(user)
    metadata = {
        "id": device["id"],
        "displayName": device["name"],
        "brightness": db.get_device_brightness_8bit(device),
        "autoDim": device["night_mode_enabled"],
    }
    return Response(json.dumps(metadata), status=200, mimetype="application/json")


def push_image(device_id: str, installation_id: str, image_bytes: bytes) -> None:
    device_webp_path = db.get_device_webp_dir(device_id)
    device_webp_path.mkdir(parents=True, exist_ok=True)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    # Generate a unique filename using the sanitized installation_id or current timestamp
    if installation_id:
        filename = f"{secure_filename(installation_id)}.webp"
    else:
        filename = f"__{int(time.time())}.webp"
    file_path = pushed_path / filename

    # Save the decoded image data to a file
    file_path.write_bytes(image_bytes)

    if installation_id:
        # add the app so it'll stay in the rotation
        db.add_pushed_app(device_id, file_path)


@bp.post("/devices/<string:device_id>/push")
def handle_push(device_id: str) -> ResponseReturnValue:
    try:
        if not validate_device_id(device_id):
            raise ValueError("Invalid device ID")

        # get api_key from Authorization header
        api_key = get_api_key_from_headers(request.headers)
        if not api_key:
            raise ValueError("Missing or invalid Authorization header")
        current_app.logger.debug(f"api_key : {api_key}")

        device = db.get_device_by_id(device_id)
        if not device or device["api_key"] != api_key:
            raise FileNotFoundError("Device not found or invalid API key")

        # get parameters from JSON data
        # can't use request.get_json() because the media type might not be set to application/json
        try:
            data: Dict[str, Any] = json.loads(request.get_data(as_text=True))
        except json.JSONDecodeError:
            abort(HTTPStatus.BAD_REQUEST, description="Invalid JSON data")
        installation_id = data.get(
            "installationID", data.get("installationId")
        )  # get both cases ID and Id
        current_app.logger.debug(f"installation_id: {installation_id}")
        image_data = data.get("image")

        if not image_data:
            raise ValueError("Missing required image data")

        try:
            image_bytes = base64.b64decode(image_data)
        except Exception as e:
            current_app.logger.error(str(e))
            raise ValueError("Invalid image data")

        push_image(device_id, installation_id, image_bytes)

        return Response("WebP received.", status=200)

    except ValueError as e:
        abort(HTTPStatus.BAD_REQUEST, description=str(e))
    except FileNotFoundError as e:
        abort(HTTPStatus.NOT_FOUND, description=str(e))
    except Exception as e:
        current_app.logger.error(f"Unexpected error: {str(e)}")
        abort(
            HTTPStatus.INTERNAL_SERVER_ERROR, description="An unexpected error occurred"
        )


########################################################################################################
@bp.delete("/devices/<string:device_id>/installations/<string:installation_id>")
def handle_delete(device_id: str, installation_id: str) -> ResponseReturnValue:
    if not validate_device_id(device_id):
        abort(HTTPStatus.BAD_REQUEST, description="Invalid device ID")

    # get api_key from Authorization header
    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(
            HTTPStatus.BAD_REQUEST,
            description="Missing or invalid Authorization header",
        )
    device = db.get_device_by_id(device_id)
    if not device or device["api_key"] != api_key:
        abort(HTTPStatus.NOT_FOUND)

    pushed_webp_path = db.get_device_webp_dir(device["id"]) / "pushed"
    if not pushed_webp_path.is_dir():
        abort(HTTPStatus.NOT_FOUND, description="Device directory not found")

    # Sanitize installation_id to prevent path traversal attacks
    installation_id = secure_filename(installation_id)

    # Generate the filename using the installation_id
    file_path = pushed_webp_path / f"{installation_id}.webp"
    current_app.logger.debug(file_path)
    if not file_path.is_file():
        abort(HTTPStatus.NOT_FOUND, description="File not found")

    # Delete the file
    file_path.unlink()

    return Response("Webp deleted.", status=200)


@bp.post("/devices/<string:device_id>/push_app")
def handle_installation_push(device_id: str) -> ResponseReturnValue:
    try:
        if not validate_device_id(device_id):
            raise ValueError("Invalid device ID")

        # get api_key from Authorization header
        api_key = get_api_key_from_headers(request.headers)
        if not api_key:
            raise ValueError("Missing or invalid Authorization header")

        device = db.get_device_by_id(device_id)
        if not device or device["api_key"] != api_key:
            raise FileNotFoundError("Device not found or invalid API key")

        user = db.get_user_by_device_id(device_id)
        if not user:
            raise FileNotFoundError("User not found")

        # Read the request body as a JSON object
        data: Dict[str, Any] = request.get_json()

        config = data.get("config")
        app_id = data.get("app_id")
        if not app_id:
            raise ValueError("Missing app data")
        if config is None:
            raise ValueError("Missing config data")

        app_details = db.get_app_details_by_id(user["username"], app_id)
        app_path_name = app_details.get("path")
        if not app_path_name:
            raise FileNotFoundError("Missing app path")

        app_path = Path(app_path_name)
        if not app_path.exists():
            raise FileNotFoundError("App not found")

        image_bytes = manager.render_app(
            app_path=app_path, config=config, webp_path=None, device=device
        )
        if image_bytes is None:
            raise RuntimeError("Rendering failed")
        if len(image_bytes) == 0:
            current_app.logger.debug("Empty image, not pushing")
            return Response("Empty image, not pushing", status=200)

        installation_id = data.get(
            "installationID", data.get("installationId")
        )  # get both cases ID and Id
        current_app.logger.debug(f"installation_id: {installation_id}")

        push_image(device_id, installation_id, image_bytes)

        return Response("App pushed.", status=200)

    except ValueError as e:
        abort(HTTPStatus.BAD_REQUEST, description=str(e))
    except FileNotFoundError as e:
        abort(HTTPStatus.NOT_FOUND, description=str(e))
    except RuntimeError as e:
        abort(HTTPStatus.INTERNAL_SERVER_ERROR, description=str(e))
