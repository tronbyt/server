"""Utility functions."""

import logging
import os
import shutil
import sqlite3
import time
from datetime import timedelta
from enum import Enum
from pathlib import Path
from typing import Any

from fastapi import Request, Response
from fastapi.responses import FileResponse
from fastapi_babel import _
from git import FetchInfo, GitCommandError, Repo
from werkzeug.utils import secure_filename

from tronbyt_server import db
from tronbyt_server.flash import flash
from tronbyt_server.git_utils import get_primary_remote, get_repo
from tronbyt_server.models import App, Device, User, ProtocolType
from tronbyt_server.pixlet import render_app as pixlet_render_app
from tronbyt_server.models.sync import SyncPayload
from tronbyt_server.sync import get_sync_manager


logger = logging.getLogger(__name__)
MODULE_ROOT = Path(__file__).parent.resolve()


class RepoStatus(Enum):
    CLONED = "cloned"
    UPDATED = "updated"
    REMOVED = "removed"
    FAILED = "failed"
    NO_CHANGE = "no_change"



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
    user: User,
) -> bytes | None:
    """Renders a pixlet app to a webp image."""
    if app_path.suffix.lower() == ".webp":
        if app_path.exists():
            image_data = app_path.read_bytes()
            if webp_path:
                webp_path.write_bytes(image_data)
            return image_data
        else:
            logger.error(f"Webp file not found at {app_path}")
            return None

    config_data = config.copy()
    add_default_config(config_data, device)
    tz = config_data.get("$tz")

    if not app_path.is_absolute():
        app_path = db.get_data_dir() / app_path

    device_interval = device.default_interval or 15
    app_interval = (app and app.display_time) or device_interval

    # Determine color filter
    # If app has a filter set and it's not INHERIT, use it.
    # Otherwise (app filter is None or INHERIT), use device filter.
    from tronbyt_server.models.app import ColorFilter

    color_filter = device.color_filter
    if app and app.color_filter and app.color_filter != ColorFilter.INHERIT:
        color_filter = app.color_filter

    # Prepare filters for pixlet
    # Only apply if we have a filter and it's not NONE
    filters = (
        {"color_filter": color_filter.value}
        if color_filter and color_filter != ColorFilter.NONE
        else None
    )

    data, messages = pixlet_render_app(
        path=app_path,
        config=config_data,
        width=64,
        height=32,
        maxDuration=app_interval * 1000,
        timeout=30000,
        image_format=0,
        supports2x=device.supports_2x(),
        filters=filters,
        tz=tz,
        locale=device.locale,
    )

    if data is None:
        logger.error("Error running pixlet render")
        return None
    if messages and app is not None:
        db.save_render_messages(db_conn, user, device, app, messages)

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
    if not app.path:
        return False

    app_path = Path(app.path)
    app_basename = f"{app.name}-{app.iname}"
    webp_device_path = db.get_device_webp_dir(device_id)
    webp_device_path.mkdir(parents=True, exist_ok=True)
    webp_path = webp_device_path / f"{app_basename}.webp"

    if app_path.suffix.lower() == ".webp":
        logger.debug(f"{app_basename} is a WebP app -- NO RENDER")
        if not webp_path.exists():
            # If the file doesn't exist in the device directory, copy it from the source.
            # This can happen if the app was added before this logic was in place,
            # or if the device's webp dir was cleared.
            if app_path.exists():
                shutil.copy(app_path, webp_path)
            else:
                logger.error(
                    f"Source WebP file not found for app {app_basename} at {app_path}"
                )
                return False
        return webp_path.exists()

    if app.pushed:
        logger.debug("Pushed App -- NO RENDER")
        return True
    now = int(time.time())

    if now - app.last_render > app.uinterval * 60:
        logger.info(f"{app_basename} -- RENDERING")
        device = user.devices[device_id]
        config = app.config.copy()
        add_default_config(config, device)

        start_time = time.monotonic()
        image = render_app(db_conn, app_path, config, webp_path, device, app, user)
        end_time = time.monotonic()
        render_duration = timedelta(seconds=end_time - start_time)

        if image is None:
            logger.error(f"Error rendering {app_basename}")
        app.empty_last_render = not image
        # set the devices pinned_app if autopin is true.
        if app.autopin and image:
            device.pinned_app = app.iname
        app.last_render = now
        app.last_render_duration = render_duration
        device.apps[app.iname] = app

        # Use granular field updates to avoid overwriting concurrent changes from web interface
        try:
            with db.db_transaction(db_conn) as cursor:
                db.update_app_field(
                    cursor, user.username, device_id, app.iname, "last_render", now
                )
                db.update_app_field(
                    cursor,
                    user.username,
                    device_id,
                    app.iname,
                    "last_render_duration",
                    _format_timedelta_iso8601(render_duration),
                )
                db.update_app_field(
                    cursor,
                    user.username,
                    device_id,
                    app.iname,
                    "empty_last_render",
                    app.empty_last_render,
                )
                if app.autopin and image:
                    db.update_device_field(
                        cursor, user.username, device_id, "pinned_app", app.iname
                    )
        except Exception as e:
            logger.error(f"Failed to update app fields for {app_basename}: {e}")

        return image is not None

    logger.info(f"{app_basename} -- NO RENDER")
    return True


def _format_timedelta_iso8601(td: timedelta) -> str:
    """Format a timedelta object to an ISO 8601 duration string (e.g., 'PT10.5S')."""
    seconds = td.total_seconds()
    return f"PT{seconds:g}S"


def send_default_image(device: Device) -> Response:
    """Send the default image."""
    return send_image(MODULE_ROOT / "static" / "images" / "default.webp", device, None)


def send_image(
    webp_path: Path,
    device: Device,
    app: App | None,
    immediate: bool = False,
    brightness: int | None = None,
    dwell_secs: int | None = None,
    stat_result: os.stat_result | None = None,
) -> Response:
    """Send an image as a response."""
    if immediate:
        with webp_path.open("rb") as f:
            response = Response(content=f.read(), media_type="image/webp")
    else:
        response = FileResponse(
            webp_path, media_type="image/webp", stat_result=stat_result
        )

    # Use provided brightness or calculate it
    b = brightness or db.get_device_brightness_percent(device)

    # Use provided dwell_secs or calculate it
    if dwell_secs is not None:
        s = dwell_secs
    else:
        device_interval = device.default_interval or 5
        s = app.display_time if app and app.display_time > 0 else device_interval

    response.headers["Cache-Control"] = "public, max-age=0, must-revalidate"
    response.headers["Tronbyt-Brightness"] = str(b)
    response.headers["Tronbyt-Dwell-Secs"] = str(s)
    if immediate:
        response.headers["Tronbyt-Immediate"] = "1"
    return response


async def push_image(
    device_id: str,
    installation_id: str | None,
    image_bytes: bytes,
    db_conn: sqlite3.Connection,
) -> None:
    """Save a pushed image and notify the device."""
    device = db.get_device_by_id(db_conn, device_id)
    if device and device.info.protocol_type == ProtocolType.WS:
        get_sync_manager().notify(device_id, SyncPayload(payload=image_bytes))

        # If it's a permanent installation, we still need to write to disk
        if installation_id:
            db.add_pushed_app(db_conn, device_id, installation_id)
            device_webp_path = db.get_device_webp_dir(device_id)
            pushed_path = device_webp_path / "pushed"
            pushed_path.mkdir(exist_ok=True)
            filename = f"{secure_filename(installation_id)}.webp"
            file_path = pushed_path / filename
            file_path.write_bytes(image_bytes)
        return

    # Fallback to file-based push for non-websocket or unknown devices
    device_webp_path = db.get_device_webp_dir(device_id)
    pushed_path = device_webp_path / "pushed"
    pushed_path.mkdir(exist_ok=True)

    if installation_id:
        filename = f"{secure_filename(installation_id)}.webp"
    else:
        filename = f"__{time.monotonic_ns()}.webp"
    file_path = pushed_path / filename

    file_path.write_bytes(image_bytes)

    if installation_id and db_conn:
        db.add_pushed_app(db_conn, device_id, installation_id)

    # No notification for non-websocket devices, as it's not needed.
