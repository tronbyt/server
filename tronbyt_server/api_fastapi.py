import base64
import json
from http import HTTPStatus
from typing import Any, Dict, Optional

import time
from pathlib import Path

from fastapi import APIRouter, Depends, Header, HTTPException, Request, Response
from werkzeug.utils import secure_filename

import asyncio
import tronbyt_server.db_fastapi as db
from tronbyt_server.main import logger
from tronbyt_server.manager_fastapi import push_new_image, render_app
from tronbyt_server.models.device import validate_device_id
from tronbyt_server.models_fastapi import App, Device

router = APIRouter(prefix="/v0")


def get_api_key_from_headers(authorization: Optional[str] = Header(None)) -> Optional[str]:
    if authorization:
        if authorization.startswith("Bearer "):
            return authorization.split(" ")[1]
        else:
            return authorization
    return None


@router.get("/devices")
async def list_devices(api_key: str = Depends(get_api_key_from_headers)):
    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    user = db.get_user_by_api_key(logger, api_key)
    if not user:
        raise HTTPException(
            status_code=HTTPStatus.UNAUTHORIZED, detail="Invalid API key"
        )

    devices = user.get("devices", {})
    metadata = [get_device_payload(device) for device in devices.values()]
    return {"devices": metadata}


def get_device_payload(device: Dict[str, Any]) -> dict[str, Any]:
    return {
        "id": device["id"],
        "displayName": device["name"],
        "brightness": db.get_device_brightness_8bit(logger, device),
        "autoDim": device.get("night_mode_enabled", False),
    }


@router.route("/devices/{device_id}", methods=["GET", "PATCH"])
async def get_device(
    device_id: str,
    request: Request,
    api_key: str = Depends(get_api_key_from_headers),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )
    user = db.get_user_by_device_id(logger, device_id)
    if not user:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)
    device = user["devices"].get(device_id)

    if not device:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    user_api_key_matches = user.get("api_key") and user["api_key"] == api_key
    device_api_key_matches = device.get("api_key") and device["api_key"] == api_key
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
            device["brightness"] = brightness
        if "autoDim" in data:
            device["night_mode_enabled"] = bool(data["autoDim"])
        db.save_user(logger, user)
    metadata = get_device_payload(device)
    return metadata


async def _push_image(
    device_id: str, installation_id: Optional[str], image_bytes: bytes
):
    device_webp_path = db.get_device_webp_dir(device_id)
    device_webp_path.mkdir(parents=True, exist_ok=True)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    if installation_id:
        filename = f"{secure_filename(installation_id)}.webp"
    else:
        filename = f"__{time.monotonic_ns()}.webp"
    file_path = pushed_path / filename

    file_path.write_bytes(image_bytes)

    if installation_id:
        db.add_pushed_app(logger, device_id, installation_id)

    await push_new_image(device_id)


@router.post("/devices/{device_id}/push")
async def handle_push(
    device_id: str,
    request: Request,
    api_key: str = Depends(get_api_key_from_headers),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(logger, device_id)
    if not device or device["api_key"] != api_key:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND,
            detail="Device not found or invalid API key",
        )

    try:
        data = await request.json()
    except json.JSONDecodeError:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid JSON data"
        )
    installation_id = data.get(
        "installationID", data.get("installationId")
    )
    image_data = data.get("image")

    if not image_data:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing required image data",
        )

    try:
        image_bytes = base64.b64decode(image_data)
    except Exception:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid image data"
        )

    await _push_image(device_id, installation_id, image_bytes)

    return Response("WebP received.", status_code=HTTPStatus.OK)


@router.get("/devices/{device_id}/installations")
async def list_installations(
    device_id: str, api_key: str = Depends(get_api_key_from_headers)
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(logger, device_id)
    if not device or device["api_key"] != api_key:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    apps = device.get("apps", {})
    installations = [
        {"id": installation_id, "appID": app_data.get("name", "")}
        for installation_id, app_data in apps.items()
    ]
    return {"installations": installations}


@router.route(
    "/devices/{device_id}/installations/{installation_id}",
    methods=["PATCH", "PUT"],
)
async def handle_patch_device_app(
    device_id: str,
    installation_id: str,
    request: Request,
    api_key: str = Depends(get_api_key_from_headers),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(logger, device_id)
    if not device or device["api_key"] != api_key:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    data = await request.json()
    if "set_enabled" in data:
        set_enabled = data["set_enabled"]
        if not isinstance(set_enabled, bool):
            return Response(
                "Invalid value for set_enabled. Must be a boolean.",
                status_code=HTTPStatus.BAD_REQUEST,
            )

        installation_id = secure_filename(installation_id)
        apps = device.get("apps", {})
        app_data = apps.get(installation_id)

        if (
            app_data is None
            or "iname" not in app_data
            or "name" not in app_data
        ):
            raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

        app = app_data
        if not app:
            raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

        if set_enabled:
            app["enabled"] = True
            app["last_render"] = 0
            if db.save_app(logger, device_id, app):
                return Response("App Enabled.", status_code=HTTPStatus.OK)
        else:
            app["enabled"] = False
            webp_path = db.get_device_webp_dir(device["id"])
            if not webp_path.is_dir():
                raise HTTPException(
                    status_code=HTTPStatus.NOT_FOUND,
                    detail="Device directory not found",
                )

            file_path = webp_path / f"{app['name']}-{installation_id}.webp"
            if file_path.is_file():
                file_path.unlink()
            if db.save_app(logger, device_id, app):
                return Response("App disabled.", status_code=HTTPStatus.OK)
        return Response(
            "Couldn't complete the operation",
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
        )
    else:
        return Response(
            "Unknown Operation", status_code=HTTPStatus.INTERNAL_SERVER_ERROR
        )


@router.delete("/devices/{device_id}/installations/{installation_id}")
async def handle_delete(
    device_id: str,
    installation_id: str,
    api_key: str = Depends(get_api_key_from_headers),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )
    device = db.get_device_by_id(logger, device_id)
    if not device or device["api_key"] != api_key:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    pushed_webp_path = db.get_device_webp_dir(device["id"]) / "pushed"
    if not pushed_webp_path.is_dir():
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND,
            detail="Device directory not found",
        )

    installation_id = secure_filename(installation_id)
    file_path = pushed_webp_path / f"{installation_id}.webp"
    if not file_path.is_file():
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="File not found"
        )

    file_path.unlink()
    return Response("Webp deleted.", status_code=HTTPStatus.OK)


@router.post("/devices/{device_id}/push_app")
async def handle_app_push(
    device_id: str,
    request: Request,
    api_key: str = Depends(get_api_key_from_headers),
):
    if not validate_device_id(device_id):
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Invalid device ID"
        )

    if not api_key:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST,
            detail="Missing or invalid Authorization header",
        )

    device = db.get_device_by_id(logger, device_id)
    if not device or device["api_key"] != api_key:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND,
            detail="Device not found or invalid API key",
        )

    user = db.get_user_by_device_id(logger, device_id)
    if not user:
        raise HTTPException(status_code=HTTPStatus.NOT_FOUND)

    data = await request.json()
    config = data.get("config")
    app_id = data.get("app_id")
    if not app_id:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Missing app data"
        )
    if config is None:
        raise HTTPException(
            status_code=HTTPStatus.BAD_REQUEST, detail="Missing config data"
        )

    app_details = db.get_app_details_by_id(logger, user["username"], app_id)
    app_path_name = app_details.get("path")
    if not app_path_name:
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="Missing app path"
        )

    app_path = Path(app_path_name)
    if not app_path.exists():
        raise HTTPException(
            status_code=HTTPStatus.NOT_FOUND, detail="App not found"
        )

    installation_id = data.get("installationID", data.get("installationId", ""))

    app = db.get_pushed_app(logger, user, device_id, installation_id)

    image_bytes = render_app(
        logger, app_path, config, None, Device(**device), App(**app)
    )
    if image_bytes is None:
        raise HTTPException(
            status_code=HTTPStatus.INTERNAL_SERVER_ERROR,
            detail="Rendering failed",
        )
    if len(image_bytes) == 0:
        return Response("Empty image, not pushing", status_code=HTTPStatus.OK)

    if installation_id:
        apps = user["devices"][device_id].setdefault("apps", {})
        apps[installation_id] = app
        db.save_user(logger, user)

    await _push_image(device_id, installation_id, image_bytes)

    return Response("App pushed.", status_code=HTTPStatus.OK)
