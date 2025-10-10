"""Database utility functions for Tronbyt Server."""

import json
import secrets
import shutil
import sqlite3
import string
from datetime import date, datetime, timedelta
from pathlib import Path
from typing import List, Literal, Optional, Union
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from flask import current_app, g
from tzlocal import get_localzone, get_localzone_name
from werkzeug.datastructures import FileStorage
from werkzeug.security import check_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server import system_apps
from tronbyt_server.models.app import App, AppMetadata
from tronbyt_server.models.device import (
    Device,
    validate_timezone,
)
from tronbyt_server.models.user import User


def init_db() -> None:
    (get_users_dir() / "admin" / "configs").mkdir(parents=True, exist_ok=True)
    conn = get_db()
    cursor = conn.cursor()
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS json_data (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT NOT NULL UNIQUE,
            data TEXT NOT NULL
        )
    """)
    cursor.execute("""
        CREATE TABLE IF NOT EXISTS meta (
            schema_version INTEGER NOT NULL
        )
    """)
    conn.commit()
    cursor.execute("SELECT * FROM json_data")
    row = cursor.fetchone()

    new_install = not row

    cursor.execute("SELECT * FROM meta")
    row = cursor.fetchone()
    if row:
        schema_version = row[0]
    else:
        schema_version = get_current_schema_version() if new_install else 0
        cursor.execute(
            "INSERT INTO meta (schema_version) VALUES (?)",
            (schema_version,),
        )

    if schema_version < get_current_schema_version():
        current_app.logger.info(
            f"Schema version {schema_version} is outdated. Migrating to version {get_current_schema_version()}"
        )

        # Perform migration tasks here
        if schema_version < 1:
            migrate_app_configs()
            migrate_app_paths()
            migrate_brightness_to_percent()
        if schema_version < 2:
            migrate_user_api_keys()
        if schema_version < 3:
            migrate_location_name_to_locality()

        cursor.execute(
            "UPDATE meta SET schema_version = ? WHERE schema_version = ?",
            (get_current_schema_version(), schema_version),
        )
        current_app.logger.info(
            f"Schema version {schema_version} migrated to {get_current_schema_version()}"
        )


def get_current_schema_version() -> int:
    """
    Retrieves the current schema version of the database.
    Increment this version when making changes to the database schema.
    Returns:
        int: The current schema version as an integer.
    """

    return 3


def migrate_app_configs() -> None:
    users = get_all_users()
    users_dir = get_users_dir()
    need_save = False
    for user in users:
        for device in user.get("devices", {}).values():
            if "" in device.get("apps", {}):
                del device["apps"][""]
                need_save = True
            for app in device.get("apps", {}).values():
                config_path = (
                    users_dir
                    / user["username"]
                    / "configs"
                    / f"{app['name']}-{app['iname']}.json"
                )
                if config_path.exists():
                    with config_path.open("r") as config_file:
                        app["config"] = json.load(config_file)
                    config_path.unlink()
                    need_save = True
        if need_save:
            save_user(user)


def migrate_app_paths() -> None:
    """
    Populates the "path" attribute for apps that were added before the "path" attribute
    was introduced. This function iterates through all users, their devices, and the apps
    associated with those devices. If an app does not have a "path" attribute, it assigns
    a default path based on the app's name or retrieves it from the app details. If any
    changes are made, the updated user data is saved.
    Steps:
    1. Retrieve all users.
    2. Iterate through each user's devices and their associated apps.
    3. Check if the "path" attribute is missing for an app.
    4. Assign a default path or fetch the path from app details.
    5. Save the user data if any changes were made.
    """

    users = get_all_users()
    need_save = False
    for user in users:
        for device in user.get("devices", {}).values():
            for app in device.get("apps", {}).values():
                if "path" not in app or app.get("path", "").startswith(
                    "/"
                ):  # regenerate any absolute paths
                    app["path"] = get_app_details_by_name(
                        user["username"], app["name"]
                    ).get("path") or str(
                        Path("system-apps")
                        / "apps"
                        / app["name"].replace("_", "")
                        / f"{app['name']}.star"
                    )
                    need_save = True
        if need_save:
            save_user(user)


def migrate_brightness_to_percent() -> None:
    """
    Migrates legacy brightness values (0-5 scale) to percentage-based values.
    This function iterates through all users and their devices, converting
    brightness and night_brightness values from the old 0-5 scale to
    percentage values (0-100).

    Steps:
    1. Retrieve all users.
    2. Iterate through each user's devices.
    3. Check if brightness values are in the old 0-5 scale.
    4. Convert them to percentage values.
    5. Save the user data if any changes were made.
    """
    users = get_all_users()
    current_app.logger.info("Migrating brightness values to percentage-based storage")

    for user in users:
        need_save = False
        for device in user.get("devices", {}).values():
            # Check if brightness is in the old 0-5 scale
            if "brightness" in device and device["brightness"] <= 5:
                old_value = device["brightness"]
                device["brightness"] = ui_scale_to_percent(old_value)
                need_save = True
                current_app.logger.debug(
                    f"Converted brightness from {old_value} to {device['brightness']}%"
                )

            # Check if night_brightness is in the old 0-5 scale
            if "night_brightness" in device and device["night_brightness"] <= 5:
                old_value = device["night_brightness"]
                device["night_brightness"] = ui_scale_to_percent(old_value)
                need_save = True
                current_app.logger.debug(
                    f"Converted night_brightness from {old_value} to {device['night_brightness']}%"
                )

        if need_save:
            current_app.logger.info(
                f"Migrating brightness for user: {user['username']}"
            )
            save_user(user)


def migrate_location_name_to_locality() -> None:
    """
    Migrates location data from old 'name' format to new 'locality' format.
    This function iterates through all users and their devices, converting
    location data that uses the old 'name' key to use the new 'locality' key.

    Steps:
    1. Retrieve all users.
    2. Iterate through each user's devices.
    3. Check if location data exists and uses the old 'name' format.
    4. Convert 'name' to 'locality' in the location data.
    5. Save the user data if any changes were made.
    """
    users = get_all_users()
    current_app.logger.info("Migrating location data from 'name' to 'locality' format")

    for user in users:
        need_save = False
        for device in user.get("devices", {}).values():
            location = device.get("location")
            if location and isinstance(location, dict) and "name" in location:
                # Convert old 'name' format to new 'locality' format
                old_name = location.pop("name")  # Remove 'name' key
                if (
                    "locality" not in location
                ):  # Only set if locality doesn't already exist
                    location["locality"] = old_name
                need_save = True
                current_app.logger.debug(
                    f"Converted location name '{old_name}' to locality for device {device.get('id', 'unknown')}"
                )

        if need_save:
            save_user(user)


def migrate_user_api_keys() -> None:
    """
    Generates API keys for existing users who don't have them.
    This migration ensures all users have API keys for API authentication.

    Steps:
    1. Retrieve all users.
    2. Check if user has an api_key field.
    3. Generate a 32-character API key if missing.
    4. Save the user data if any changes were made.
    """
    users = get_all_users()
    current_app.logger.info("Migrating users to add API keys")

    for user in users:
        if not user.get("api_key"):
            api_key = "".join(
                secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
            )
            user["api_key"] = api_key
            current_app.logger.info(f"Generated API key for user: {user['username']}")
            save_user(user)


def get_db() -> sqlite3.Connection:
    db = getattr(g, "_database", None)
    if db is None:
        db = g._database = sqlite3.connect(
            current_app.config["DB_FILE"], autocommit=True
        )
    return db


def delete_device_dirs(device_id: str) -> None:
    # Get the name of the current app
    app_name = current_app.name

    # Construct the path to the directory to delete
    dir_to_delete = Path(app_name) / "webp" / device_id

    # Delete the directory recursively
    try:
        shutil.rmtree(dir_to_delete)
        current_app.logger.debug(f"Successfully deleted directory: {dir_to_delete}")
    except FileNotFoundError:
        current_app.logger.error(f"Directory not found: {dir_to_delete}")
    except Exception as e:
        current_app.logger.error(f"Error deleting directory {dir_to_delete}: {str(e)}")


def get_last_app_index(device_id: str) -> Optional[int]:
    device = get_device_by_id(device_id)
    if device is None:
        return None
    return device.get("last_app_index", 0)


def save_last_app_index(device_id: str, index: int) -> None:
    user = get_user_by_device_id(device_id)
    if user is None:
        return
    device = user["devices"].get(device_id)
    if device is None:
        return
    device["last_app_index"] = index
    save_user(user)


def get_device_timezone(device: Device) -> ZoneInfo:
    """Get timezone in order of precedence: location -> device -> local timezone."""
    if location := device.get("location"):
        if tz := location.get("timezone"):
            if zi := validate_timezone(tz):
                return zi

    # Legacy timezone handling
    if tz := device.get("timezone"):
        if zi := validate_timezone(tz):
            return zi
        elif isinstance(tz, int):
            # Convert integer offset to a valid timezone name
            hours_offset = int(tz)
            sign = "+" if hours_offset >= 0 else "-"
            return ZoneInfo(f"Etc/GMT{sign}{abs(hours_offset)}")

    # Default to the server's local timezone
    return get_localzone()


def get_device_timezone_str(device: Device) -> str:
    zone_info = get_device_timezone(device)
    return zone_info.key or get_localzone_name()


def get_night_mode_is_active(device: Device) -> bool:
    if not device.get("night_mode_enabled", False):
        return False

    # get_device_timezone will always return a valid tz string
    now = datetime.now(get_device_timezone(device))
    current_time_minutes = now.hour * 60 + now.minute

    # Parse start and end times
    start_time = device.get("night_start")
    end_time = device.get("night_end")

    # Handle legacy integer format (hours only) and convert to HH:MM
    if isinstance(start_time, int):
        if start_time < 0:
            return False
        start_time = f"{start_time:02d}:00"
    elif not start_time:
        return False

    if isinstance(end_time, int):
        end_time = f"{end_time:02d}:00"
    elif not end_time:
        end_time = "06:00"  # default to 6 am if not set

    # Parse time strings to minutes since midnight
    try:
        start_parts = start_time.split(":")
        start_minutes = int(start_parts[0]) * 60 + int(start_parts[1])

        end_parts = end_time.split(":")
        end_minutes = int(end_parts[0]) * 60 + int(end_parts[1])
    except (ValueError, IndexError, AttributeError):
        current_app.logger.warning(
            f"Invalid night mode time format: start={start_time}, end={end_time}"
        )
        return False

    # Determine if night mode is active
    if start_minutes <= end_minutes:  # Normal case (e.g., 9:00 to 17:00)
        if start_minutes <= current_time_minutes < end_minutes:
            current_app.logger.debug("Night mode active")
            return True
    else:  # Wrapped case (e.g., 22:00 to 7:00 - overnight)
        if current_time_minutes >= start_minutes or current_time_minutes < end_minutes:
            current_app.logger.debug("Night mode active")
            return True

    return False


def get_dim_mode_is_active(device: Device) -> bool:
    """Check if dim mode is active (dimming without full night mode)."""
    dim_time = device.get("dim_time")
    if not dim_time:
        return False

    # get_device_timezone will always return a valid tz string
    now = datetime.now(get_device_timezone(device))
    current_time_minutes = now.hour * 60 + now.minute

    # Parse dim start time string to minutes since midnight
    try:
        dim_parts = dim_time.split(":")
        dim_start_minutes = int(dim_parts[0]) * 60 + int(dim_parts[1])
    except (ValueError, IndexError, AttributeError):
        current_app.logger.warning(f"Invalid dim time format: {dim_time}")
        return False

    # Determine dim end time using night_end (if set) or default to 6am
    dim_end_minutes = None

    # Check if night_end is set (regardless of whether night mode is enabled)
    night_end = device.get("night_end")
    if night_end:
        # Handle legacy integer format
        if isinstance(night_end, int):
            if night_end >= 0:
                dim_end_minutes = night_end * 60
        else:
            try:
                night_end_parts = night_end.split(":")
                dim_end_minutes = int(night_end_parts[0]) * 60 + int(night_end_parts[1])
            except (ValueError, IndexError, AttributeError):
                pass

    # If no night_end, default to 6am (360 minutes)
    if dim_end_minutes is None:
        dim_end_minutes = 6 * 60

    # Check if current time is within dim period
    if dim_start_minutes <= dim_end_minutes:  # Normal case (e.g., 20:00 to 22:00)
        if dim_start_minutes <= current_time_minutes < dim_end_minutes:
            current_app.logger.debug(
                f"Dim mode active (normal case): {dim_start_minutes} <= {current_time_minutes} < {dim_end_minutes}"
            )
            return True
    else:  # Wrapped case (e.g., 22:00 to 06:00 - overnight)
        if (
            current_time_minutes >= dim_start_minutes
            or current_time_minutes < dim_end_minutes
        ):
            current_app.logger.debug(
                f"Dim mode active (wrapped case): {current_time_minutes} >= {dim_start_minutes} or < {dim_end_minutes}"
            )
            return True

    return False


# Get the brightness percentage value to send to firmware
def get_device_brightness_8bit(device: Device) -> int:
    # Priority: night mode > dim mode > normal brightness
    # If we're in night mode, use night_brightness if available
    if get_night_mode_is_active(device):
        return device.get("night_brightness", 1)
    # If we're in dim mode (but not night mode), use dim_brightness
    elif get_dim_mode_is_active(device):
        return device.get("dim_brightness", device.get("brightness", 50))
    else:
        return device.get("brightness", 50)


# Convert percentage brightness (0-100) to UI scale (0-5)
def percent_to_ui_scale(percent: int) -> int:
    if percent == 0:
        return 0
    elif percent <= 3:
        return 1
    elif percent <= 12:
        return 2
    elif percent <= 20:
        return 3
    elif percent <= 35:
        return 4
    else:
        return 5


# Convert UI scale (0-5) to percentage (0-100)
def ui_scale_to_percent(scale_value: int) -> int:
    lookup = {
        0: 0,
        1: 3,
        2: 12,
        3: 20,
        4: 35,
        5: 100,
    }
    # Handle legacy brightness if needed
    if scale_value in lookup:
        return lookup[scale_value]
    return 20  # Default to level 3 (20%)


# For API compatibility - map from 8bit values to 0-5 scale
def brightness_map_8bit_to_levels(brightness: int) -> int:
    return percent_to_ui_scale(brightness)


def get_data_dir() -> Path:
    return Path(current_app.config["DATA_DIR"]).absolute()


def get_users_dir() -> Path:
    return Path(current_app.config["USERS_DIR"]).absolute()


def get_user(username: str) -> Optional[User]:
    try:
        conn = get_db()
        cursor = conn.cursor()
        cursor.execute(
            "SELECT data FROM json_data WHERE username = ?", (str(username),)
        )
        row = cursor.fetchone()
        if row:
            user: User = json.loads(row[0])
            if "theme_preference" not in user:
                user["theme_preference"] = "system"  # Default for existing users
            return user
        else:
            current_app.logger.error(f"{username} not found")
            return None
    except Exception as e:
        current_app.logger.error("problem with get_user" + str(e))
        return None


def auth_user(username: str, password: str) -> Optional[Union[User, bool]]:
    user = get_user(username)
    if user:
        password_hash = user.get("password")
        if password_hash and check_password_hash(password_hash, password):
            current_app.logger.debug(f"returning {user}")
            return user
        else:
            current_app.logger.info("bad password")
            return False
    return None


def save_user(user: User, new_user: bool = False) -> bool:
    if "username" not in user:
        current_app.logger.warning("no username in user")
        return False
    if current_app.testing:
        current_app.logger.debug(f"user data passed to save_user : {user}")
    username = user["username"]
    try:
        conn = get_db()
        cursor = conn.cursor()
        # json
        if new_user:
            cursor.execute(
                "INSERT INTO json_data (data, username) VALUES (?, ?)",
                (json.dumps(user), str(username)),
            )
            create_user_dir(username)
        else:
            cursor.execute(
                "UPDATE json_data SET data = ? WHERE username = ?",
                (json.dumps(user), str(username)),
            )
        conn.commit()

        return True
    except Exception as e:
        current_app.logger.error("couldn't save {} : {}".format(user, str(e)))
        return False


def delete_user(username: str) -> bool:
    try:
        conn = get_db()
        conn.cursor().execute("DELETE FROM json_data WHERE username = ?", (username,))
        conn.commit()
        user_dir = get_users_dir() / username
        if user_dir.exists():
            shutil.rmtree(user_dir)
        current_app.logger.info(f"User {username} deleted successfully")
        return True
    except Exception as e:
        current_app.logger.error(f"Error deleting user {username}: {e}")
        return False


def create_user_dir(user: str) -> None:
    # create the user directory if it doesn't exist
    user_dir = get_users_dir() / secure_filename(user)
    (user_dir / "configs").mkdir(parents=True, exist_ok=True)
    (user_dir / "apps").mkdir(parents=True, exist_ok=True)


def get_apps_list(user: str) -> List[AppMetadata]:
    app_list: List[AppMetadata] = list()
    # test for directory named dir and if not exist create it
    if user == "system" or user == "":
        list_file = get_data_dir() / "system-apps.json"
        if not list_file.exists():
            current_app.logger.info("Generating apps.json file...")
            system_apps.update_system_repo(get_data_dir(), current_app.logger)
            current_app.logger.debug("apps.json file generated.")

        with list_file.open("r") as f:
            app_list = json.load(f)
            return app_list

    dir = get_users_dir() / user / "apps"
    if dir.exists():
        for file in dir.rglob("*.star"):
            app_name = file.stem
            # Get file modification time
            mod_time = datetime.fromtimestamp(file.stat().st_mtime)
            app_dict = AppMetadata(
                path=str(file),
                id=app_name,
                name=app_name,
                date=mod_time.strftime("%Y-%m-%d %H:%M"),
            )
            preview = get_data_dir() / "apps" / f"{app_name}.webp"
            if preview.exists() and preview.stat().st_size > 0:
                app_dict["preview"] = str(preview.name)
            yaml_path = file.parent / "manifest.yaml"
            # current_app.logger.debug(f"checking for manifest.yaml in {yaml_path}")
            if yaml_path.exists():
                with yaml_path.open("r") as f:
                    yaml_dict = yaml.safe_load(f)
                    app_dict.update(yaml_dict)
            else:
                app_dict["summary"] = "Custom App"
            app_list.append(app_dict)
        return app_list
    else:
        # current_app.logger.warning(f"no apps list found for {user}")
        return []


def get_app_details(user: str, field: Literal["id", "name"], value: str) -> AppMetadata:
    # first look for the app name in the custom apps
    custom_apps = get_apps_list(user)
    for app in custom_apps:
        if app.get(field) == value:
            # we found it
            current_app.logger.debug(f"returning details for {app}")
            return app
        # Also check fileName if looking up by name (with or without .star extension)
        if field == "name":
            file_name = app.get("fileName") or ""
            # Check both with and without .star extension
            file_name_base = file_name.removesuffix(".star")
            if file_name == value or file_name_base == value:
                current_app.logger.debug(
                    f"returning details for {app} (matched by fileName)"
                )
                return app
    # if we get here then the app is not in custom apps
    # so we need to look in the system-apps directory
    apps = get_apps_list("system")
    for app in apps:
        if app.get(field) == value:
            return app
        # Also check fileName if looking up by name (with or without .star extension)
        if field == "name":
            file_name = app.get("fileName") or ""
            # Check both with and without .star extension
            file_name_base = file_name.removesuffix(".star")
            if file_name == value or file_name_base == value:
                current_app.logger.debug(
                    f"returning details for {app} (matched by fileName)"
                )
                return app
    return {}


def get_app_details_by_name(user: str, name: str) -> AppMetadata:
    return get_app_details(user, "name", name)


def get_app_details_by_id(user: str, id: str) -> AppMetadata:
    return get_app_details(user, "id", id)


def sanitize_url(url: str) -> str:
    # Decode any percent-encoded characters
    url = unquote(url)
    # Replace spaces with underscores
    url = url.replace(" ", "_")
    # Remove unwanted characters
    for char in ["'", "\\"]:
        url = url.replace(char, "")
    # Encode back into a valid URL
    url = quote(url, safe="/:.?&=")  # Allow standard URL characters
    return url


def allowed_file(filename: str) -> bool:
    return filename.lower().endswith(".star")


def save_user_app(file: FileStorage, path: Path) -> bool:
    filename = file.filename
    if not filename:
        return False
    filename = secure_filename(filename)
    if file and allowed_file(filename):
        file.save(path / filename)
        return True
    else:
        return False


def delete_user_upload(user: User, filename: str) -> bool:
    user_apps_path = get_users_dir() / user["username"] / "apps"

    try:
        filename = secure_filename(filename)
        folder_name = Path(filename).stem
        file_path = user_apps_path / folder_name / filename
        folder_path = user_apps_path / folder_name

        if not str(file_path).startswith(str(user_apps_path)):
            current_app.logger.warning("Security warning: Attempted path traversal")
            return False

        if folder_path.exists():
            shutil.rmtree(folder_path)

        return True
    except OSError as e:
        current_app.logger.error(f"couldn't delete file: {e}")
        return False


def get_all_users() -> List[User]:
    conn = get_db()
    cursor = conn.cursor()
    cursor.execute("SELECT data FROM json_data")
    return [json.loads(row[0]) for row in cursor.fetchall()]


def has_users() -> bool:
    """Checks if any users exist in the database."""
    conn = get_db()
    cursor = conn.cursor()
    cursor.execute("SELECT 1 FROM json_data LIMIT 1")
    return cursor.fetchone() is not None


def get_is_app_schedule_active(app: App, device: Device) -> bool:
    current_time = datetime.now(get_device_timezone(device))
    return get_is_app_schedule_active_at_time(app, current_time)


def get_is_app_schedule_active_at_time(app: App, current_time: datetime) -> bool:
    """Check if app should be active at the given time using either legacy or new recurrence system."""
    # Check time range first
    start_time = datetime.strptime(
        str(app.get("start_time") or "00:00"), "%H:%M"
    ).time()
    end_time = datetime.strptime(str(app.get("end_time") or "23:59"), "%H:%M").time()

    current_time_only = current_time.replace(second=0, microsecond=0).time()
    if start_time > end_time:
        in_time_range = current_time_only >= start_time or current_time_only <= end_time
    else:
        in_time_range = start_time <= current_time_only <= end_time

    if not in_time_range:
        return False

    # Use custom recurrence system only if explicitly enabled
    if app.get("use_custom_recurrence", False) and app.get("recurrence_type"):
        return _is_recurrence_active_at_time(app, current_time)
    else:
        # Default to legacy daily schedule system
        current_day = current_time.strftime("%A").lower()
        active_days = app.get(
            "days",
            [
                "monday",
                "tuesday",
                "wednesday",
                "thursday",
                "friday",
                "saturday",
                "sunday",
            ],
        )
        return isinstance(active_days, list) and current_day in active_days


def _is_recurrence_active_at_time(app: App, current_time: datetime) -> bool:
    """Check if app recurrence pattern matches the current time."""
    recurrence_type = app.get("recurrence_type", "daily")
    recurrence_interval = app.get("recurrence_interval", 1)
    recurrence_pattern = app.get("recurrence_pattern", [])
    recurrence_start_date_str = app.get("recurrence_start_date")
    recurrence_end_date_str = app.get("recurrence_end_date")

    # Parse start date
    if not recurrence_start_date_str:
        # Default to a reasonable start date if not specified
        recurrence_start_date = datetime(2025, 1, 1).date()
    else:
        try:
            recurrence_start_date = datetime.strptime(
                recurrence_start_date_str, "%Y-%m-%d"
            ).date()
        except ValueError:
            return False

    # Check end date
    if recurrence_end_date_str:
        try:
            recurrence_end_date = datetime.strptime(
                recurrence_end_date_str, "%Y-%m-%d"
            ).date()
            if current_time.date() > recurrence_end_date:
                return False
        except ValueError:
            pass

    current_date = current_time.date()

    if recurrence_type == "daily":
        # Every X days
        days_since_start = (current_date - recurrence_start_date).days
        return days_since_start >= 0 and days_since_start % recurrence_interval == 0

    elif recurrence_type == "weekly":
        # Every X weeks on specified weekdays
        weeks_since_start = (current_date - recurrence_start_date).days // 7
        if weeks_since_start < 0 or weeks_since_start % recurrence_interval != 0:
            return False

        # Check if current weekday is in the pattern
        if isinstance(recurrence_pattern, dict) and recurrence_pattern.get("weekdays"):
            weekdays = recurrence_pattern["weekdays"]
        elif isinstance(recurrence_pattern, list) and recurrence_pattern:
            weekdays = recurrence_pattern
        else:
            weekdays = [
                "monday",
                "tuesday",
                "wednesday",
                "thursday",
                "friday",
                "saturday",
                "sunday",
            ]

        current_weekday = current_time.strftime("%A").lower()
        return current_weekday in weekdays

    elif recurrence_type == "monthly":
        # Every X months on specified day or weekday pattern
        months_since_start = _months_between_dates(recurrence_start_date, current_date)
        if months_since_start < 0 or months_since_start % recurrence_interval != 0:
            return False

        if isinstance(recurrence_pattern, dict):
            if "day_of_month" in recurrence_pattern:
                # Specific day of month (e.g., 1st, 15th)
                return current_date.day == recurrence_pattern["day_of_month"]
            elif "day_of_week" in recurrence_pattern:
                # Specific weekday pattern (e.g., "first_monday", "last_friday")
                return _matches_monthly_weekday_pattern(
                    current_date, recurrence_pattern["day_of_week"]
                )

        return True  # If no specific pattern, match any day in the month

    elif recurrence_type == "yearly":
        # Every X years - matches same month and day as start date
        years_since_start = current_date.year - recurrence_start_date.year
        if years_since_start < 0 or years_since_start % recurrence_interval != 0:
            return False

        # Match same month and day as start date
        return (
            current_date.month == recurrence_start_date.month
            and current_date.day == recurrence_start_date.day
        )

    return False


def _months_between_dates(start_date: date, end_date: date) -> int:
    """Calculate number of months between two dates."""
    return (end_date.year - start_date.year) * 12 + (end_date.month - start_date.month)


def _matches_monthly_weekday_pattern(target_date: date, pattern: str) -> bool:
    """Check if date matches a monthly weekday pattern like 'first_monday' or 'last_friday'."""
    try:
        parts = pattern.split("_")
        if len(parts) != 2:
            return False

        occurrence, weekday = parts
        target_weekday = weekday.lower()

        # Get weekday index (0=Monday, 6=Sunday)
        weekday_map = {
            "monday": 0,
            "tuesday": 1,
            "wednesday": 2,
            "thursday": 3,
            "friday": 4,
            "saturday": 5,
            "sunday": 6,
        }

        if target_weekday not in weekday_map:
            return False

        target_weekday_index = weekday_map[target_weekday]
        current_weekday_index = target_date.weekday()

        if current_weekday_index != target_weekday_index:
            return False

        # Find which occurrence of this weekday this date represents
        if occurrence == "last":
            # Check if this is the last occurrence of this weekday in the month
            next_week = target_date + timedelta(days=7)
            return next_week.month != target_date.month
        else:
            # Find the Nth occurrence of this weekday in the month
            occurrence_count = 0
            for day in range(1, target_date.day + 1):
                test_date = target_date.replace(day=day)
                if test_date.weekday() == target_weekday_index:
                    occurrence_count += 1

            occurrence_map = {"first": 1, "second": 2, "third": 3, "fourth": 4}

            return occurrence_map.get(occurrence, 0) == occurrence_count

    except (AttributeError, ValueError, TypeError) as e:
        # AttributeError: if pattern doesn't have split() method or date operations fail
        # ValueError: if target_date.replace() gets invalid day values
        # TypeError: if pattern is None or wrong type
        try:
            current_app.logger.warning(
                f"Invalid monthly weekday pattern '{pattern}' for date {target_date}: {e}"
            )
        except RuntimeError:
            # Outside application context, skip logging
            pass
        return False


def get_device_by_name(user: User, name: str) -> Optional[Device]:
    for device in user.get("devices", {}).values():
        if device.get("name") == name:
            return device
    return None


def get_device_webp_dir(device_id: str, create: bool = True) -> Path:
    path = get_data_dir() / "webp" / secure_filename(device_id)
    if not path.exists() and create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def get_device_by_id(device_id: str) -> Optional[Device]:
    for user in get_all_users():
        device = user.get("devices", {}).get(device_id)
        if device:
            return device
    return None


def get_user_by_device_id(device_id: str) -> Optional[User]:
    for user in get_all_users():
        device = user.get("devices", {}).get(device_id)
        if device:
            return user
    return None


def get_firmware_version() -> Optional[str]:
    """Get the current firmware version from the downloaded firmware."""
    version_file = get_data_dir() / "firmware" / "firmware_version.txt"
    try:
        if version_file.exists():
            with version_file.open("r") as f:
                return f.read().strip()
    except Exception as e:
        current_app.logger.error(f"Error reading firmware version: {e}")
    return None


def get_user_by_api_key(api_key: str) -> Optional[User]:
    for user in get_all_users():
        if user.get("api_key") == api_key:
            return user
    return None


def get_pushed_app(user: User, device_id: str, installation_id: str) -> App:
    apps = user["devices"][device_id].setdefault("apps", {})
    if installation_id in apps:
        # already in there
        return apps[installation_id]
    app = App(
        iname=installation_id,
        name="pushed",
        uinterval=10,
        display_time=0,
        notes="",
        enabled=True,
        pushed=True,
        order=len(apps),
    )
    return app


def add_pushed_app(device_id: str, installation_id: str) -> None:
    user = get_user_by_device_id(device_id)
    if not user:
        raise ValueError("User not found")

    app = get_pushed_app(user, device_id, installation_id)
    apps = user["devices"][device_id].setdefault("apps", {})
    apps[installation_id] = app
    save_user(user)


def save_app(device_id: str, app: App) -> bool:
    try:
        # first get the user from the device, should already be validated elsewhere
        user = get_user_by_device_id(device_id)
        if not user:
            return False
        if not app["iname"]:
            # don't save apps without an instance name
            # this can happen when an app is pushed, but fails to render
            return True
        # user.get("devices",{}).get("apps",{})
        user["devices"][device_id]["apps"][app["iname"]] = app
        save_user(user)
        return True
    except Exception:
        return False


def save_render_messages(device: Device, app: App, messages: List[str]) -> None:
    """Save render messages from pixlet to the app configuration.

    Args:
        device: The device configuration
        app: The app configuration
        messages: List of messages from pixlet render output
    """
    # Get the app from device and update its messages
    app["render_messages"] = messages

    # Save the updated app
    if not save_app(device["id"], app):
        current_app.logger.error("Error saving render messages: Failed to save app.")
