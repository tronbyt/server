"""API router."""

import base64
import hashlib
import logging
import sqlite3
import time
from pathlib import Path
from typing import Any

from fastapi import APIRouter, Depends, HTTPException, Request, status
from fastapi.responses import JSONResponse, Response
from pydantic import BaseModel
from werkzeug.utils import secure_filename

from tronbyt_server import db
from tronbyt_server.dependencies import get_db, get_user_and_device_from_api_key
from tronbyt_server.utils import push_new_image, render_app
from tronbyt_server.models import App, Device, DeviceID, User

router = APIRouter(prefix="/v0", tags=["api"])
logger = logging.getLogger(__name__)

# Constants for dots SVG generation
DEFAULT_DOTS_WIDTH = 64
DEFAULT_DOTS_HEIGHT = 32
DEFAULT_DOTS_RADIUS = 0.3
MAX_DOTS_DIMENSION = 256


class DeviceUpdate(BaseModel):
    brightness: int | None = None
    autoDim: bool | None = None


class PushData(BaseModel):
    installationID: str | None = None
    installationId: str | None = None
    image: str


class PatchDeviceData(BaseModel):
    set_enabled: bool | None = None
    set_pinned: bool | None = None


class PushAppData(BaseModel):
    config: dict[str, Any]
    app_id: str
    installationID: str | None = None
    installationId: str | None = None


def get_device_payload(device: Device) -> dict[str, Any]:
    return {
        "id": device.id,
        "displayName": device.name,
        "brightness": db.get_device_brightness_8bit(device),
        "autoDim": device.night_mode_enabled,
    }


@router.get("/dots")
def generate_dots_svg(
    request: Request,
    w: int = DEFAULT_DOTS_WIDTH,
    h: int = DEFAULT_DOTS_HEIGHT,
    r: float = DEFAULT_DOTS_RADIUS,
) -> Response:
    """Generate an SVG mask pattern with dots."""
    # ETag generation
    etag_content = f"{w}-{h}-{r}"
    etag = f'"{hashlib.md5(etag_content.encode()).hexdigest()}"'

    # Check If-None-Match header
    if request.headers.get("if-none-match") == etag:
        return Response(status_code=status.HTTP_304_NOT_MODIFIED)

    # Validate width
    if w <= 0 or w > MAX_DOTS_DIMENSION:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=f"Parameter 'w' must be between 1 and {MAX_DOTS_DIMENSION}",
        )

    # Validate height
    if h <= 0 or h > MAX_DOTS_DIMENSION:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail=f"Parameter 'h' must be between 1 and {MAX_DOTS_DIMENSION}",
        )

    # Validate radius
    if r <= 0 or r > 1:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST,
            detail="Parameter 'r' must be between 0 and 1",
        )

    # Generate SVG with dots
    data = [
        '<?xml version="1.0" encoding="UTF-8"?>',
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" fill="#fff">',
    ]

    # Pre-compute radius string to avoid repeated formatting
    radius_str = f"{r:g}"
    for y in range(h):
        y_str = f"{y + 0.5:g}"
        for x in range(w):
            x_str = f"{x + 0.5:g}"
            data.append(f'<circle cx="{x_str}" cy="{y_str}" r="{radius_str}"/>')

    data.append("</svg>\n")

    headers = {
        "Cache-Control": "public, max-age=31536000",
        "ETag": etag,
    }

    return Response(content="".join(data), media_type="image/svg+xml", headers=headers)


@router.get("/devices", response_model=dict[str, list[dict[str, Any]]])
def list_devices(
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> dict[str, list[dict[str, Any]]]:
    user, _ = auth
    if not user:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="User not found"
        )
    devices = user.devices
    metadata = [get_device_payload(device) for device in devices.values()]
    return {"devices": metadata}


@router.get("/devices/{device_id}")
def get_device(
    device_id: DeviceID,
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> dict[str, Any]:
    _, device = auth
    if not device or device.id != device_id:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )
    return get_device_payload(device)


@router.patch("/devices/{device_id}")
def update_device(
    device_id: DeviceID,
    data: DeviceUpdate,
    db_conn: sqlite3.Connection = Depends(get_db),
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> dict[str, Any]:
    user, device = auth
    if not user or not device or device.id != device_id:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Device not found"
        )

    if data.brightness is not None:
        if not 0 <= data.brightness <= 255:
            raise HTTPException(
                status_code=status.HTTP_400_BAD_REQUEST,
                detail="Brightness must be between 0 and 255",
            )
        device.brightness = data.brightness
    if data.autoDim is not None:
        device.night_mode_enabled = data.autoDim

    user.devices[device_id] = device
    db.save_user(db_conn, user)
    return get_device_payload(user.devices[device_id])


def _push_image(
    device_id: str,
    installation_id: str | None,
    image_bytes: bytes,
    db_conn: sqlite3.Connection | None = None,
) -> None:
    device_webp_path = db.get_device_webp_dir(device_id)
    device_webp_path.mkdir(parents=True, exist_ok=True)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    if installation_id:
        filename = f"{secure_filename(installation_id)}.webp"
    else:
        filename = f"__{time.monotonic_ns()}.webp"
    file_path = pushed_path / filename

    print(f"Writing pushed image to {file_path}")
    file_path.write_bytes(image_bytes)

    if installation_id and db_conn:
        db.add_pushed_app(db_conn, device_id, installation_id)

    push_new_image(device_id, logger)


@router.post("/devices/{device_id}/push")
def handle_push(
    device_id: DeviceID,
    data: PushData,
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    _, device = auth
    if not device or device.id != device_id:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Device not found or invalid API key",
        )

    installation_id = data.installationID or data.installationId
    try:
        image_bytes = base64.b64decode(data.image)
    except Exception:
        raise HTTPException(
            status_code=status.HTTP_400_BAD_REQUEST, detail="Invalid image data"
        )

    _push_image(device_id, installation_id, image_bytes, db_conn)
    return Response("WebP received.", status_code=status.HTTP_200_OK)


@router.get("/devices/{device_id}/installations")
def list_installations(
    device_id: DeviceID,
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> Response:
    _, device = auth
    if not device or device.id != device_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    apps = device.apps
    installations = [
        {"id": installation_id, "appID": app_data.name}
        for installation_id, app_data in apps.items()
    ]
    return JSONResponse(content={"installations": installations})


@router.patch("/devices/{device_id}/installations/{installation_id}")
def handle_patch_device_app(
    device_id: DeviceID,
    installation_id: str,
    data: PatchDeviceData,
    db_conn: sqlite3.Connection = Depends(get_db),
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> Response:
    user, device = auth
    if not user or not device or device.id != device_id:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Device not found or invalid API key",
        )

    installation_id = secure_filename(installation_id)
    apps = device.apps
    app = apps.get(installation_id)

    if not app:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App not found"
        )

    # Handle the set_enabled command
    if data.set_enabled is not None:
        if data.set_enabled:
            app.enabled = True
            app.last_render = 0
            if db.save_app(db_conn, device_id, app):
                return Response("App Enabled.", status_code=status.HTTP_200_OK)
        else:
            app.enabled = False
            webp_path = db.get_device_webp_dir(device.id)
            file_path = webp_path / f"{app.name}-{installation_id}.webp"
            if file_path.is_file():
                file_path.unlink()
            if db.save_app(db_conn, device_id, app):
                return Response("App disabled.", status_code=status.HTTP_200_OK)

    if data.set_pinned is not None:
        if data.set_pinned:
            device.pinned_app = installation_id
            message = "App pinned."
        elif device.pinned_app == installation_id:
            device.pinned_app = None
            message = "App unpinned."
        else:
            return Response("App is not pinned.", status_code=status.HTTP_200_OK)

        user.devices[device_id] = device
        if db.save_user(db_conn, user):
            return Response(message, status_code=status.HTTP_200_OK)

    raise HTTPException(
        status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        detail="Couldn't complete the operation",
    )


@router.delete("/devices/{device_id}/installations/{installation_id}")
def handle_delete(
    device_id: DeviceID,
    installation_id: str,
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> Response:
    _, device = auth
    if not device or device.id != device_id:
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    pushed_webp_path = db.get_device_webp_dir(device.id) / "pushed"
    installation_id = secure_filename(installation_id)
    file_path = pushed_webp_path / f"{installation_id}.webp"

    if not file_path.is_file():
        raise HTTPException(status_code=status.HTTP_404_NOT_FOUND)

    file_path.unlink()
    return Response("Webp deleted.", status_code=status.HTTP_200_OK)


@router.post("/devices/{device_id}/push_app")
def handle_app_push(
    device_id: DeviceID,
    data: PushAppData,
    db_conn: sqlite3.Connection = Depends(get_db),
    auth: tuple[User | None, Device | None] = Depends(get_user_and_device_from_api_key),
) -> Response:
    user, device = auth
    if not user or not device or device.id != device_id:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND,
            detail="Device not found or invalid API key",
        )

    app_details = db.get_app_details_by_id(db_conn, user.username, data.app_id)
    app_path_name = app_details.get("path")
    if not app_path_name:
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="Missing app path"
        )
    app_path = Path(app_path_name)
    if not app_path.exists():
        raise HTTPException(
            status_code=status.HTTP_404_NOT_FOUND, detail="App not found"
        )

    installation_id = data.installationID or data.installationId or ""
    app_dict = db.get_pushed_app(user, device_id, installation_id)
    app = App(**app_dict)
    image_bytes = render_app(db_conn, app_path, data.config, None, device, app, logger)
    if image_bytes is None:
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="Rendering failed",
        )
    if len(image_bytes) == 0:
        return Response("Empty image, not pushing", status_code=status.HTTP_200_OK)

    if installation_id:
        apps = user.devices[device_id].apps
        apps[installation_id] = app
        db.save_user(db_conn, user)

    _push_image(device_id, installation_id, image_bytes, db_conn)
    return Response("App pushed.", status_code=status.HTTP_200_OK)
