"""
This module provides the API endpoints for the Tronbyt server.

It includes endpoints for managing devices and their installations.
"""
import asyncio
import base64
import json
import time
from http import HTTPStatus
from pathlib import Path
from typing import Any, Dict, Optional

from fastapi import APIRouter, Depends, Header, HTTPException, Request, Response
from werkzeug.utils import secure_filename

from tronbyt_server import db
from tronbyt_server.manager import push_new_image, render_app
from tronbyt_server.models import App, Device, User
from tronbyt_server.auth import get_user_from_api_key

router = APIRouter()


def get_device_payload(device: Device) -> dict[str, Any]:
    return {
        "id": device.id,
        "displayName": device.name,
        "brightness": db.get_device_brightness_8bit(device.model_dump()),
        "autoDim": device.night_mode_enabled,
    }


@router.get("/devices")
async def list_devices(user: User = Depends(get_user_from_api_key)):
    devices = user.devices.values()
    metadata = [get_device_payload(device) for device in devices]
    return {"devices": metadata}


@router.get("/devices/{device_id}")
@router.patch("/devices/{device_id}")
async def get_device(
    device_id: str,
    request: Request,
    authorization: Optional[str] = Header(None),
):
    if not db.validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    user = db.get_user_by_device_id(db.logger, device_id)
    if not user:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    device = user["devices"].get(device_id)

    if not device:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    user_api_key_matches = user.get("api_key") and user["api_key"] == authorization
    device_api_key_matches = (
        device.get("api_key") and device["api_key"] == authorization
    )
    if not user_api_key_matches and not device_api_key_matches:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    if request.method == "PATCH":
        data = await request.json()
        if "brightness" in data:
            brightness = int(data["brightness"])
            if brightness < 0 or brightness > 255:
                raise HTTPException(
                    status_code=HTTPStatus.BAD_REQUEST,
                    detail="Brightness must be between 0 and 255",
                )
            # Store only the percentage value
            device["brightness"] = brightness
        if "autoDim" in data:
            device["night_mode_enabled"] = bool(data["autoDim"])
        db.save_user(db.logger, user)

    metadata = get_device_payload(Device(**device))
    return metadata


def _push_image(
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
        db.add_pushed_app(db.logger, device_id, installation_id)

    asyncio.create_task(push_new_image(device_id))


@router.post("/devices/{device_id}/push")
async def handle_push(
    device_id: str,
    request: Request,
    authorization: Optional[str] = Header(None),
):
    try:
        if not db.validate_device_id(device_id):
            raise ValueError("Invalid device ID")

        # get api_key from Authorization header
        if not authorization:
            raise ValueError("Missing or invalid Authorization header")

        device = db.get_device_by_id(db.logger, device_id)
        if not device or device["api_key"] != authorization:
            raise FileNotFoundError("Device not found or invalid API key")

        # get parameters from JSON data
        try:
            data: Dict[str, Any] = await request.json()
        except json.JSONDecodeError:
            raise HTTPException(
                status_code=HTTPStatus.BAD_REQUEST, detail="Invalid JSON data"
            )
        installation_id = data.get(
            "installationID", data.get("installationId")
        )  # get both cases ID and Id
        db.logger.debug(f"installation_id: {installation_id}")
        image_data = data.get("image")

        if not image_data:
            raise ValueError("Missing required image data")

        try:
            image_bytes = base64.b64decode(image_data)
        except Exception as e:
            db.logger.error(str(e))
            raise ValueError("Invalid image data")

        _push_image(device_id, installation_id, image_bytes)

        return Response("WebP received.", status_code=200)

    except ValueError as e:
        raise HTTPException(status_code=HTTPStatus.BAD_REQUEST, detail=str(e))
    except FileNotFoundError as e:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail=str(e))
    except Exception as e:
        db.logger.error(f"Unexpected error: {str(e)}")
        raise HTTPException(
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
            detail="An unexpected error occurred",
        )


@router.get("/devices/{device_id}/installations")
async def list_installations(
    device_id: str,
    authorization: Optional[str] = Header(None),
):
    if not db.validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    # get api_key from Authorization header
    if not authorization:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(db.logger, device_id)
    if not device or device["api_key"] != authorization:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    apps = device.get("apps", {})
    installations = [
        {"id": installation_id, "appID": app_data.get("name", "")}
        for installation_id, app_data in apps.items()
    ]
    return {"installations": installations}


@router.patch("/devices/{device_id}/installations/{installation_id}")
@router.put("/devices/{device_id}/installations/{installation_id}")
async def handle_patch_device_app(
    device_id: str,
    installation_id: str,
    request: Request,
    authorization: Optional[str] = Header(None),
):
    if not db.validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    # get api_key from Authorization header
    if not authorization:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(db.logger, device_id)
    if not device or device["api_key"] != authorization:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    # Handle the set_enabled json command
    data = await request.json()
    if "set_enabled" in data:
        set_enabled = data["set_enabled"]
        if not isinstance(set_enabled, bool):
            return Response(
                "Invalid value for set_enabled. Must be a boolean.", status_code=400
            )

        # Sanitize installation_id to prevent path traversal attacks
        installation_id = secure_filename(installation_id)
        apps = device.get("apps", {})

        # Get app_data and immediately return if it's not a valid dictionary
        app_data: Optional[App] = apps.get(installation_id)

        if app_data is None or "iname" not in app_data or "name" not in app_data:
            raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

        # Proceed with using app_data safely
        app: App = app_data
        if not app:
            raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

        # Enable it. Should probably render it right away too.
        if set_enabled:
            app["enabled"] = True
            app["last_render"] = 0  # this will trigger render on next fetch
            if db.save_app(db.logger, device_id, app):
                return Response("App Enabled.", status_code=200)

        else:
            app["enabled"] = False
            webp_path = db.get_device_webp_dir(device["id"])
            if not webp_path.is_dir():
                raise HTTPException(
                    status_code=HTTPStatus.NOT_FOUND,
                    detail="Device directory not found",
                )

            # Generate the filename using the installation_id eg. Acidwarp-220.webp
            file_path = webp_path / f"{app['name']}-{installation_id}.webp"
            db.logger.debug(file_path)
            if file_path.is_file():
                # Delete the file
                file_path.unlink()
            if db.save_app(db.logger, device_id, app):
                return Response("App disabled.", status_code=200)
        return Response("Couldn't complete the operation", status_code=500)
    else:
        return Response("Unknown Operation", status_code=500)


@router.delete("/devices/{device_id}/installations/{installation_id}")
async def handle_delete(
    device_id: str,
    installation_id: str,
    authorization: Optional[str] = Header(None),
):
    if not db.validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    # get api_key from Authorization header
    if not authorization:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )
    device = db.get_device_by_id(db.logger, device_id)
    if not device or device["api_key"] != authorization:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    pushed_webp_path = db.get_device_webp_dir(device["id"]) / "pushed"
    if not pushed_webp_path.is_dir():
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Device directory not found"
        )

    # Sanitize installation_id to prevent path traversal attacks
    installation_id = secure_filename(installation_id)

    # Generate the filename using the installation_id
    file_path = pushed_webp_path / f"{installation_id}.webp"
    db.logger.debug(file_path)
    if not file_path.is_file():
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail="File not found")

    # Delete the file
    file_path.unlink()

    return Response("Webp deleted.", status_code=200)


@router.post("/devices/{device_id}/push_app")
async def handle_app_push(
    device_id: str,
    request: Request,
    authorization: Optional[str] = Header(None),
):
    try:
        if not db.validate_device_id(device_id):
            raise ValueError("Invalid device ID")

        # get api_key from Authorization header
        if not authorization:
            raise ValueError("Missing or invalid Authorization header")

        device = db.get_device_by_id(db.logger, device_id)
        if not device or device["api_key"] != authorization:
            raise FileNotFoundError("Device not found or invalid API key")

        user = db.get_user_by_device_id(db.logger, device_id)
        if not user:
            raise FileNotFoundError("User not found")

        # Read the request body as a JSON object
        data: Dict[str, Any] = await request.json()

        config = data.get("config")
        app_id = data.get("app_id")
        if not app_id:
            raise ValueError("Missing app data")
        if config is None:
            raise ValueError("Missing config data")

        app_details = db.get_app_details_by_id(db.logger, user["username"], app_id)
        app_path_name = app_details.get("path")
        if not app_path_name:
            raise FileNotFoundError("Missing app path")

        app_path = Path(app_path_name)
        if not app_path.exists():
            raise FileNotFoundError("App not found")

        installation_id = data.get(
            "installationID", data.get("installationId", "")
        )  # get both cases ID and Id
        db.logger.debug(f"installation_id: {installation_id}")

        app = db.get_pushed_app(db.logger, user, device_id, installation_id)

        image_bytes = render_app(db.logger, app_path, config, None, device, app)
        if image_bytes is None:
            raise RuntimeError("Rendering failed")
        if len(image_bytes) == 0:
            db.logger.debug("Empty image, not pushing")
            return Response("Empty image, not pushing", status_code=200)

        if installation_id:
            apps = user["devices"][device_id].setdefault("apps", {})
            apps[installation_id] = app
            db.save_user(db.logger, user)

        _push_image(device_id, installation_id, image_bytes)

        return Response("App pushed.", status_code=200)

    except ValueError as e:
        raise HTTPException(status_code=HTTPStatus.BAD_REQUEST, detail=str(e))
    except FileNotFoundError as e:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND, detail=str(e))
    except RuntimeError as e:
        raise HTTPException(
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR, detail=str(e)
        )
