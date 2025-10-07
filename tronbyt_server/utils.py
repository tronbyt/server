"""Utility functions."""

import logging
import os
import subprocess
import time
import sqlite3
from pathlib import Path
from typing import Any

from fastapi import Request, Response
from fastapi.responses import FileResponse
from werkzeug.utils import secure_filename

from tronbyt_server import db
from tronbyt_server.config import settings
from tronbyt_server.flash import flash
from tronbyt_server.models import App, Device, User
from tronbyt_server.pixlet import render_app as pixlet_render_app
from tronbyt_server.sync import get_sync_manager


def git_command(
    command: list[str], cwd: Path | None = None, check: bool = False
) -> subprocess.CompletedProcess[bytes]:
    """Run a git command in the specified path."""
    env = os.environ.copy()
    env.setdefault("HOME", os.getcwd())
    return subprocess.run(command, cwd=cwd, env=env, check=check)


def server_root() -> str:
    """Get the root URL of the server."""
    protocol = settings.SERVER_PROTOCOL
    hostname = settings.SERVER_HOSTNAME_OR_IP
    port = settings.SERVER_PORT
    url = f"{protocol}://{hostname}"
    if (protocol == "https" and port != "443") or (protocol == "http" and port != "80"):
        url += f":{port}"
    return url


def ws_root() -> str:
    """Get the root URL for websockets."""
    server_protocol = settings.SERVER_PROTOCOL
    protocol = "wss" if server_protocol == "https" else "ws"
    hostname = settings.SERVER_HOSTNAME_OR_IP
    port = settings.SERVER_PORT
    url = f"{protocol}://{hostname}"
    if (protocol == "wss" and port != "443") or (protocol == "ws" and port != "80"):
        url += f":{port}"
    return url


def add_default_config(config: dict[str, Any], device: Device) -> dict[str, Any]:
    """Add default configuration values to an app's config."""
    config["$tz"] = db.get_device_timezone_str(device)
    return config


def render_app(
    db_conn: sqlite3.Connection,
    app_path: Path,
    config: dict[str, Any],
    webp_path: Path | None,
    device: Device,
    app: App | None,
    logger: logging.Logger,
) -> bytes | None:
    """Renders a pixlet app to a webp image."""
    config_data = config.copy()
    add_default_config(config_data, device)

    if not app_path.is_absolute():
        app_path = db.get_data_dir() / app_path

    magnify = 1
    width = 64
    height = 32

    if device.supports_2x():
        magnify = 2
        if app:
            user = db.get_user_by_device_id(db_conn, device.id)
            if user and app.id:
                app_details = db.get_app_details_by_id(db_conn, user.username, app.id)
                if app_details.get("supports2x", False):
                    magnify = 1
                    width = 128
                    height = 64

    data, messages = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=width,
        height=height,
        magnify=magnify,
        maxDuration=15000,
        timeout=30000,
        image_format=0,
        logger=logger,
    )
    messages = (
        messages if isinstance(messages, list) else [messages] if messages else []
    )

    if data is None:
        logger.error("Error running pixlet render")
        return None
    if messages is not None and app is not None:
        db.save_render_messages(db_conn, device, app, messages)

    if len(data) > 0 and webp_path:
        webp_path.write_bytes(data)
    return data


def set_repo(
    db_conn: sqlite3.Connection,
    request: Request,
    user: User,
    repo_name: str,
    apps_path: Path,
    repo_url: str,
) -> bool:
    """Clone or update a git repository."""
    if repo_url != "":
        old_repo = getattr(user, repo_name, "")
        if old_repo != repo_url:
            repo_url = "/".join(
                secure_filename(part) for part in repo_url.split("/")[-2:]
            )
            setattr(user, repo_name, repo_url)
            db.save_user(db_conn, user)

            if apps_path.exists():
                import shutil

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
                flash(request, "Repo Cloned")
                return True
            else:
                flash(request, "Error Cloning Repo")
                return False
        else:
            result = git_command(["git", "-C", str(apps_path), "pull"])
            if result.returncode == 0:
                flash(request, "Repo Updated")
                return True
            else:
                flash(request, "Repo Update Failed")
                return False
    else:
        flash(request, "No Changes to Repo")
        return True


def possibly_render(
    db_conn: sqlite3.Connection,
    user: User,
    device_id: str,
    app: App,
    logger: logging.Logger,
) -> bool:
    """Render an app if it's time to do so."""
    if app.pushed:
        logger.debug("Pushed App -- NO RENDER")
        return True
    now = int(time.time())
    app_basename = f"{app.name}-{app.iname}"
    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app_basename}.webp"
    if not app.path:
        return False
    app_path = Path(app.path)

    if now - app.last_render > app.uinterval * 60:
        logger.debug(f"RENDERING -- {app_basename}")
        device = user.devices[device_id]
        config = app.config.copy()
        add_default_config(config, device)
        image = render_app(db_conn, app_path, config, webp_path, device, app, logger)
        if image is None:
            logger.error(f"Error rendering {app_basename}")
        app.empty_last_render = len(image) == 0 if image is not None else False
        app.last_render = now
        db.save_app(db_conn, device_id, app)

        return image is not None

    logger.debug(f"{app_basename} -- NO RENDER")
    return True


def send_default_image(device: Device) -> Response:
    """Send the default image."""
    return send_image(Path("tronbyt_server/static/images/default.webp"), device, None)


def send_image(
    webp_path: Path, device: Device, app: App | None, immediate: bool = False
) -> Response:
    """Send an image as a response."""
    if immediate:
        with webp_path.open("rb") as f:
            response = Response(content=f.read(), media_type="image/webp")
    else:
        response = FileResponse(webp_path, media_type="image/webp")
    b = db.get_device_brightness_8bit(device)
    device_interval = device.default_interval
    s = app.display_time if app and app.display_time > 0 else device_interval
    response.headers["Tronbyt-Brightness"] = str(b)
    response.headers["Tronbyt-Dwell-Secs"] = str(s)
    return response


def push_new_image(device_id: str, logger: logging.Logger) -> None:
    """Wake up WebSocket loops to push a new image to a given device."""
    get_sync_manager(logger).notify(device_id)
