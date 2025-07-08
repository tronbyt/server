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
from tronbyt_server.manager import push_new_image, render_app
from tronbyt_server.models.app import App
from tronbyt_server.models.device import Device, validate_device_id

bp = Blueprint("api", __name__, url_prefix="/v0")


def get_api_key_from_headers(headers: Headers) -> Optional[str]:
    auth_header = headers.get("Authorization")
    if auth_header:
        if auth_header.startswith("Bearer "):
            return auth_header.split(" ")[1]
        else:
            return auth_header
    return None


def get_device_payload(device: Device) -> dict[str, Any]:
    return {
        "id": device["id"],
        "displayName": device["name"],
        "brightness": db.get_device_brightness_8bit(device),
        "autoDim": device.get("night_mode_enabled", False),
    }


@bp.route("/devices", methods=["GET"])
def list_devices() -> ResponseReturnValue:
    api_key = get_api_key_from_headers(request.headers)
    if not api_key:
        abort(
            HTTPStatus.BAD_REQUEST,
            description="Missing or invalid Authorization header",
        )

    user = db.get_user_by_api_key(api_key)
    if not user:
        abort(HTTPStatus.UNAUTHORIZED, description="Invalid API key")

    devices = user.get("devices", {})
    metadata = [get_device_payload(device) for device in devices.values()]
    return Response(
        json.dumps({"devices": metadata}),
        status=200,
        mimetype="application/json",
    )


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
    user = db.get_user_by_device_id(device_id)
    if not user:
        abort(HTTPStatus.NOT_FOUND)
    device = user["devices"].get(device_id)

    if not device:
        abort(HTTPStatus.NOT_FOUND)

    user_api_key_matches = user.get("api_key") and user["api_key"] == api_key
    device_api_key_matches = device.get("api_key") and device["api_key"] == api_key
    if not user_api_key_matches and not device_api_key_matches:
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
            # Store only the percentage value
            device["brightness"] = brightness
        if "autoDim" in data:
            device["night_mode_enabled"] = bool(data["autoDim"])
        db.save_user(user)
    metadata = get_device_payload(device)
    return Response(json.dumps(metadata), status=200, mimetype="application/json")


def push_image(
    device_id: str, installation_id: Optional[str], image_bytes: bytes
) -> None:
    device_webp_path = db.get_device_webp_dir(device_id)
    device_webp_path.mkdir(parents=True, exist_ok=True)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    # Generate a unique filename using the sanitized installation_id or current timestamp
    if installation_id:
        filename = f"{secure_filename(installation_id)}.webp"
    else:
        filename = f"__{time.monotonic_ns()}.webp"
    file_path = pushed_path / filename

    # Save the decoded image data to a file
    file_path.write_bytes(image_bytes)

    if installation_id:
        # add the app so it'll stay in the rotation
        db.add_pushed_app(device_id, installation_id)

    push_new_image(device_id)


@bp.post("/devices/<string:device_id>/push")
def handle_push(device_id: str) -> ResponseReturnValue:
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


@bp.get("/devices/<string:device_id>/installations")
def list_installations(device_id: str) -> ResponseReturnValue:
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

    apps = device.get("apps", {})
    installations = [
        {"id": installation_id, "appID": app_data.get("name", "")}
        for installation_id, app_data in apps.items()
    ]
    return Response(
        json.dumps({"installations": installations}),
        status=200,
        mimetype="application/json",
    )


########################################################################################################
@bp.route(
    "/devices/<string:device_id>/installations/<string:installation_id>",
    methods=["PATCH", "PUT"],
)
def handle_patch_device_app(
    device_id: str, installation_id: str
) -> ResponseReturnValue:
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

    # Handle the set_enabled json command
    if request.json is not None and "set_enabled" in request.json:
        set_enabled = request.json["set_enabled"]
        if not isinstance(set_enabled, bool):
            return Response(
                "Invalid value for set_enabled. Must be a boolean.", status=400
            )

        # Sanitize installation_id to prevent path traversal attacks
        installation_id = secure_filename(installation_id)
        apps = device.get("apps", {})

        # Get app_data and immediately return if it's not a valid dictionary
        app_data: Optional[App] = apps.get(installation_id)

        if app_data is None or "iname" not in app_data or "name" not in app_data:
            abort(HTTPStatus.NOT_FOUND)

        # Proceed with using app_data safely
        app: App = app_data
        if not app:
            abort(HTTPStatus.NOT_FOUND)

        # Enable it. Should probably render it right away too.
        if set_enabled:
            app["enabled"] = True
            app["last_render"] = 0  # this will trigger render on next fetch
            if db.save_app(device_id, app):
                return Response("App Enabled.", status=200)

        else:
            app["enabled"] = False
            webp_path = db.get_device_webp_dir(device["id"])
            if not webp_path.is_dir():
                abort(HTTPStatus.NOT_FOUND, description="Device directory not found")

            # Generate the filename using the installation_id eg. Acidwarp-220.webp
            file_path = webp_path / f"{app['name']}-{installation_id}.webp"
            current_app.logger.debug(file_path)
            if file_path.is_file():
                # Delete the file
                file_path.unlink()
            if db.save_app(device_id, app):
                return Response("App disabled.", status=200)
        return Response("Couldn't complete the operation", status=500)
    else:
        return Response("Unknown Operation", status=500)


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
def handle_app_push(device_id: str) -> ResponseReturnValue:
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

        installation_id = data.get(
            "installationID", data.get("installationId", "")
        )  # get both cases ID and Id
        current_app.logger.debug(f"installation_id: {installation_id}")

        app = db.get_pushed_app(user, device_id, installation_id)

        image_bytes = render_app(app_path, config, None, device, app)
        if image_bytes is None:
            raise RuntimeError("Rendering failed")
        if len(image_bytes) == 0:
            current_app.logger.debug("Empty image, not pushing")
            return Response("Empty image, not pushing", status=200)

        if installation_id:
            apps = user["devices"][device_id].setdefault("apps", {})
            apps[installation_id] = app
            db.save_user(user)

        push_image(device_id, installation_id, image_bytes)

        return Response("App pushed.", status=200)

    except ValueError as e:
        abort(HTTPStatus.BAD_REQUEST, description=str(e))
    except FileNotFoundError as e:
        abort(HTTPStatus.NOT_FOUND, description=str(e))
    except RuntimeError as e:
        abort(HTTPStatus.INTERNAL_SERVER_ERROR, description=str(e))
