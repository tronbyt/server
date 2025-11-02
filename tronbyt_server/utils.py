"""Utility functions."""

import logging
import shutil
import sqlite3
import time
from enum import Enum
from pathlib import Path
from typing import Any

from fastapi import Request, Response
from fastapi.responses import FileResponse
from fastapi_babel import _
from git import FetchInfo, GitCommandError, Repo

from tronbyt_server import db
from tronbyt_server.flash import flash
from tronbyt_server.git_utils import get_primary_remote, get_repo
from tronbyt_server.models import App, Device, User
from tronbyt_server.pixlet import render_app as pixlet_render_app
from tronbyt_server.sync import get_sync_manager


logger = logging.getLogger(__name__)


class RepoStatus(Enum):
    CLONED = "cloned"
    UPDATED = "updated"
    REMOVED = "removed"
    FAILED = "failed"
    NO_CHANGE = "no_change"


def add_default_config(config: dict[str, Any], device: Device) -> dict[str, Any]:
    """Add default configuration values to an app's config."""
    config["$tz"] = db.get_device_timezone_str(device)
    config["$2x"] = str(device.supports_2x()).lower()
    return config


def render_app(
    db_conn: sqlite3.Connection,
    app_path: Path,
    config: dict[str, Any],
    webp_path: Path | None,
    device: Device,
    app: App | None,
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
                app_details = db.get_app_details_by_id(user.username, app.id)
                if app_details and app_details.supports2x:
                    magnify = 1
                    width = 128
                    height = 64

    device_interval = device.default_interval or 15
    app_interval = (app and app.display_time) or device_interval

    data, messages_raw = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=width,
        height=height,
        magnify=magnify,
        maxDuration=app_interval * 1000,
        timeout=30000,
        image_format=0,
    )
    messages: list[str] = []
    if isinstance(messages_raw, list):
        messages = messages_raw
    elif messages_raw:
        messages = [messages_raw]

    if data is None:
        logger.error("Error running pixlet render")
        return None
    if messages and app is not None:
        db.save_render_messages(db_conn, device, app, messages)

    if len(data) > 0 and webp_path:
        webp_path.write_bytes(data)
    return data


def set_repo(
    request: Request,
    apps_path: Path,
    old_repo_url: str,
    repo_url: str,
) -> bool:
    """Clone or update a git repository."""
    status = RepoStatus.NO_CHANGE
    if repo_url:
        repo = get_repo(apps_path)
        # If repo URL has changed, or path is not a valid git repo, then re-clone.
        if old_repo_url != repo_url or not repo:
            if apps_path.exists():
                shutil.rmtree(apps_path)
            try:
                Repo.clone_from(repo_url, apps_path, depth=1)
                status = RepoStatus.CLONED
            except GitCommandError:
                status = RepoStatus.FAILED
        else:
            # Repo exists and URL is the same, so pull changes.
            try:
                remote = get_primary_remote(repo)
                if remote:
                    fetch_info = remote.pull()
                    if all(info.flags & FetchInfo.HEAD_UPTODATE for info in fetch_info):
                        status = RepoStatus.NO_CHANGE
                    else:
                        status = RepoStatus.UPDATED
                else:
                    logger.warning(
                        f"No remote found to pull from for repo at {apps_path}"
                    )
                    status = RepoStatus.FAILED
            except GitCommandError:
                status = RepoStatus.FAILED

    elif old_repo_url:
        if apps_path.exists():
            shutil.rmtree(apps_path)
        status = RepoStatus.REMOVED

    messages = {
        RepoStatus.CLONED: _("Repo Cloned"),
        RepoStatus.UPDATED: _("Repo Updated"),
        RepoStatus.REMOVED: _("Repo removed"),
        RepoStatus.FAILED: _("Error Cloning or Updating Repo"),
        RepoStatus.NO_CHANGE: _("No Changes to Repo"),
    }
    flash(request, messages[status])
    return status != RepoStatus.FAILED


def possibly_render(
    db_conn: sqlite3.Connection,
    user: User,
    device_id: str,
    app: App,
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
        logger.info(f"{app_basename} -- RENDERING")
        device = user.devices[device_id]
        config = app.config.copy()
        add_default_config(config, device)
        image = render_app(db_conn, app_path, config, webp_path, device, app)
        if image is None:
            logger.error(f"Error rendering {app_basename}")
        app.empty_last_render = len(image) == 0 if image is not None else False
        # set the devices pinned_app if autopin is true.
        if app.autopin and not app.empty_last_render:
            device.pinned_app = app.iname
            db.save_user(db_conn, user)
        app.last_render = now
        db.save_app(db_conn, device_id, app)

        return image is not None

    logger.info(f"{app_basename} -- NO RENDER")
    return True


def send_default_image(device: Device) -> Response:
    """Send the default image."""
    return send_image(Path("tronbyt_server/static/images/default.webp"), device, None)


def send_image(
    webp_path: Path,
    device: Device,
    app: App | None,
    immediate: bool = False,
    brightness: int | None = None,
    dwell_secs: int | None = None,
) -> Response:
    """Send an image as a response."""
    if immediate:
        with webp_path.open("rb") as f:
            response = Response(content=f.read(), media_type="image/webp")
    else:
        response = FileResponse(webp_path, media_type="image/webp")
    # Use provided brightness or calculate it
    b = brightness or db.get_device_brightness_percent(device)

    # Use provided dwell_secs or calculate it
    if dwell_secs is not None:
        s = dwell_secs
    else:
        device_interval = device.default_interval or 5
        s = app.display_time if app and app.display_time > 0 else device_interval
    response.headers["Tronbyt-Brightness"] = str(b)
    response.headers["Tronbyt-Dwell-Secs"] = str(s)
    if immediate:
        response.headers["Tronbyt-Immediate"] = "1"
    return response


def push_new_image(device_id: str) -> None:
    """Wake up WebSocket loops to push a new image to a given device."""
    get_sync_manager().notify(device_id)
