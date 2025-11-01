"Database utility functions for Tronbyt Server."

import json
import logging
import secrets
import shutil
import sqlite3
import string
from datetime import date, datetime, timedelta
from pathlib import Path
from typing import Any, Literal
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from fastapi import UploadFile
from tzlocal import get_localzone, get_localzone_name
from werkzeug.security import check_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server import system_apps
from tronbyt_server.config import get_settings
from tronbyt_server.models import App, AppMetadata, Device, User, Weekday

logger = logging.getLogger(__name__)


def get_db() -> sqlite3.Connection:
    """Get a database connection."""
    return sqlite3.connect(get_settings().DB_FILE, check_same_thread=False)


def init_db(db: sqlite3.Connection) -> None:
    """Initialize the database."""
    cursor = db.cursor()
    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS json_data (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            username TEXT NOT NULL UNIQUE,
            data TEXT NOT NULL
        )
    """
    )
    cursor.execute(
        """
        CREATE TABLE IF NOT EXISTS meta (
            schema_version INTEGER NOT NULL
        )
    """
    )
    db.commit()
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
        logger.info(
            f"Schema version {schema_version} is outdated. Migrating to version {get_current_schema_version()}"
        )

        if schema_version < 1:
            migrate_app_configs(db)
            migrate_app_paths(db)
            migrate_brightness_to_percent(db)
        if schema_version < 2:
            migrate_user_api_keys(db)
        if schema_version < 3:
            migrate_location_name_to_locality(db)
        if schema_version < 4:
            migrate_recurrence_pattern(db)

        cursor.execute(
            "UPDATE meta SET schema_version = ? WHERE schema_version = ?",
            (get_current_schema_version(), schema_version),
        )
        logger.info(
            f"Schema version {schema_version} migrated to {get_current_schema_version()}"
        )


def get_current_schema_version() -> int:
    """
    Retrieves the current schema version of the database.
    Increment this version when making changes to the database schema.
    Returns:
        int: The current schema version as an integer.
    """

    return 4


def migrate_recurrence_pattern(db: sqlite3.Connection) -> None:
    """Migrate recurrence patterns from lists to dictionaries."""
    logger.info("Migrating recurrence patterns")
    cursor = db.cursor()
    cursor.execute("SELECT username, data FROM json_data")
    rows = cursor.fetchall()

    for username, data_json in rows:
        try:
            user_data = json.loads(data_json)
            need_save = False

            if "devices" in user_data and isinstance(user_data["devices"], dict):
                for device in user_data["devices"].values():
                    if "apps" in device and isinstance(device["apps"], dict):
                        for app in device["apps"].values():
                            if "recurrence_pattern" in app and isinstance(
                                app["recurrence_pattern"], list
                            ):
                                app[
                                    "recurrence_pattern"
                                ] = {}  # Convert empty list to empty dict
                                need_save = True
                                logger.debug(
                                    f"Converted recurrence_pattern for app {app.get('name')} in device {device.get('name')}"
                                )

            if need_save:
                logger.info(f"Migrating recurrence patterns for user: {username}")
                updated_data_json = json.dumps(user_data)
                db.cursor().execute(
                    "UPDATE json_data SET data = ? WHERE username = ?",
                    (updated_data_json, username),
                )
                db.commit()

        except json.JSONDecodeError as e:
            logger.error(f"Error decoding JSON for user {username}: {e}")
        except Exception as e:
            logger.error(f"An unexpected error occurred for user {username}: {e}")


def migrate_app_configs(db: sqlite3.Connection) -> None:
    """Migrate app configs from individual files to the user's JSON data."""
    users = get_all_users(db)
    users_dir = get_users_dir()
    need_save = False
    for user in users:
        for device in user.devices.values():
            if "" in device.apps:
                del device.apps[""]
                need_save = True
            for app in device.apps.values():
                config_path = (
                    users_dir
                    / user.username
                    / "configs"
                    / f"{app.name}-{app.iname}.json"
                )
                if config_path.exists():
                    with config_path.open("r") as config_file:
                        app.config = json.load(config_file)
                    config_path.unlink()
                    need_save = True
        if need_save:
            save_user(db, user)


def migrate_app_paths(db: sqlite3.Connection) -> None:
    """Populate the 'path' attribute for apps that are missing it."""
    users = get_all_users(db)
    need_save = False
    for user in users:
        for device in user.devices.values():
            for app in device.apps.values():
                if not app.path or app.path.startswith("/"):
                    app_details = get_app_details_by_name(user.username, app.name)
                    app.path = (
                        app_details.path
                        if app_details
                        else str(
                            Path("system-apps")
                            / "apps"
                            / app.name.replace("_", "")
                            / f"{app.name}.star"
                        )
                    )
                    need_save = True
        if need_save:
            save_user(db, user)


def migrate_brightness_to_percent(db: sqlite3.Connection) -> None:
    """Migrate legacy brightness values to percentage-based values."""
    users = get_all_users(db)
    logger.info("Migrating brightness values to percentage-based storage")

    for user in users:
        need_save = False
        for device in user.devices.values():
            # Check if brightness is in the old 0-5 scale
            if device.brightness <= 5:
                old_value = device.brightness
                device.brightness = ui_scale_to_percent(old_value)
                need_save = True
                logger.debug(
                    f"Converted brightness from {old_value} to {device.brightness}%"
                )

            if device.night_brightness <= 5:
                old_value = device.night_brightness
                device.night_brightness = ui_scale_to_percent(old_value)
                need_save = True
                logger.debug(
                    f"Converted night_brightness from {old_value} to {device.night_brightness}%"
                )

        if need_save:
            logger.info(f"Migrating brightness for user: {user.username}")
            save_user(db, user)


def migrate_location_name_to_locality(db: sqlite3.Connection) -> None:
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
    users = get_all_users(db)
    logger.info("Migrating location data from 'name' to 'locality' format")

    for user in users:
        need_save = False
        for device in user.devices.values():
            location = device.location
            if location and location.name:
                # Convert old 'name' format to new 'locality' format
                location.locality = location.name
                location.name = None
                need_save = True
                logger.debug(
                    f"Converted location name '{location.name}' to locality for device {device.id}"
                )

        if need_save:
            save_user(db, user)


def migrate_user_api_keys(db: sqlite3.Connection) -> None:
    """Generate API keys for users who don't have them."""
    users = get_all_users(db)
    logger.info("Migrating users to add API keys")

    for user in users:
        if not user.api_key:
            user.api_key = "".join(
                secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
            )
            logger.info(f"Generated API key for user: {user.username}")
            save_user(db, user)


def close_db(db: sqlite3.Connection) -> None:
    """Close the database connection."""
    db.close()


def delete_device_dirs(device_id: str) -> None:
    """Delete the directories associated with a device."""
    webp_dir = get_data_dir() / "webp"
    dir_to_delete = webp_dir / secure_filename(device_id)
    # Ensure dir_to_delete is within the expected webp directory to prevent path traversal
    try:
        dir_to_delete.relative_to(webp_dir)
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in device_id")
        return

    try:
        shutil.rmtree(dir_to_delete)
        logger.debug(f"Successfully deleted directory: {dir_to_delete}")
    except FileNotFoundError:
        logger.error(f"Directory not found: {dir_to_delete}")
    except Exception as e:
        logger.error(f"Error deleting directory {dir_to_delete}: {str(e)}")


def get_last_app_index(db: sqlite3.Connection, device_id: str) -> int | None:
    """Get the last app index for a device."""
    device = get_device_by_id(db, device_id)
    if device is None:
        return None
    return device.last_app_index


def save_last_app_index(db: sqlite3.Connection, device_id: str, index: int) -> None:
    """Save the last app index for a device."""
    user = get_user_by_device_id(db, device_id)
    if user is None:
        return
    device = user.devices.get(device_id)
    if device is None:
        return
    device.last_app_index = index
    save_user(db, user)


def get_device_timezone(device: Device) -> ZoneInfo:
    """Get timezone for a device."""
    if device.location and device.location.timezone:
        try:
            return ZoneInfo(device.location.timezone)
        except Exception:
            pass
    if device.timezone:
        try:
            return ZoneInfo(device.timezone)
        except Exception:
            pass
    return get_localzone()


def get_device_timezone_str(device: Device) -> str:
    """Get the timezone string for a device."""
    zone_info = get_device_timezone(device)
    return zone_info.key or get_localzone_name()


def get_night_mode_is_active(device: Device) -> bool:
    """Check if night mode is active for a device."""
    if not device.night_mode_enabled:
        return False

    # get_device_timezone will always return a valid tz string
    now = datetime.now(get_device_timezone(device))
    current_time_minutes = now.hour * 60 + now.minute

    # Parse start and end times
    start_time = device.night_start
    end_time = device.night_end

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
        logger.warning(
            f"Invalid night mode time format: start={start_time}, end={end_time}"
        )
        return False

    # Determine if night mode is active
    if start_minutes <= end_minutes:  # Normal case (e.g., 9:00 to 17:00)
        if start_minutes <= current_time_minutes < end_minutes:
            logger.debug("Night mode active")
            return True
    else:  # Wrapped case (e.g., 22:00 to 7:00 - overnight)
        if current_time_minutes >= start_minutes or current_time_minutes < end_minutes:
            logger.debug("Night mode active")
            return True

    return False


def get_dim_mode_is_active(device: Device) -> bool:
    """Check if dim mode is active (dimming without full night mode)."""
    dim_time = device.dim_time
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
        logger.warning(f"Invalid dim time format: {dim_time}")
        return False

    # Determine dim end time using night_end (if set) or default to 6am
    dim_end_minutes = None

    # Check if night_end is set (regardless of whether night mode is enabled)
    night_end = device.night_end
    if night_end:
        # Handle legacy integer format
        if isinstance(night_end, int):
            if night_end >= 0:
                dim_end_minutes = int(night_end) * 60
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
            logger.debug(
                f"Dim mode active (normal case): {dim_start_minutes} <= {current_time_minutes} < {dim_end_minutes}"
            )
            return True
    else:  # Wrapped case (e.g., 22:00 to 06:00 - overnight)
        if (
            current_time_minutes >= dim_start_minutes
            or current_time_minutes < dim_end_minutes
        ):
            logger.debug(
                f"Dim mode active (wrapped case): {current_time_minutes} >= {dim_start_minutes} or < {dim_end_minutes}"
            )
            return True

    return False


# Get the brightness percentage value to send to firmware
def get_device_brightness_percent(device: Device) -> int:
    """Get the device brightness on an 8-bit scale."""
    # Priority: night mode > dim mode > normal brightness
    # If we're in night mode, use night_brightness if available
    if get_night_mode_is_active(device):
        return device.night_brightness if device.night_brightness is not None else 1
    # If we're in dim mode (but not night mode), use dim_brightness
    elif get_dim_mode_is_active(device):
        if device.dim_brightness is not None:
            return device.dim_brightness
        return device.brightness if device.brightness is not None else 50
    else:
        return device.brightness if device.brightness is not None else 50


def percent_to_ui_scale(percent: int) -> int:
    """Convert percentage brightness to UI scale."""
    if percent == 0:
        return 0
    elif percent <= 3:
        return 1
    elif percent <= 5:
        return 2
    elif percent <= 12:
        return 3
    elif percent <= 35:
        return 4
    else:
        return 5


def ui_scale_to_percent(scale_value: int) -> int:
    lookup = {
        0: 0,
        1: 3,
        2: 5,
        3: 12,
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
    """Get the data directory."""
    return Path(get_settings().DATA_DIR).absolute()


def get_users_dir() -> Path:
    """Get the users directory."""
    return Path(get_settings().USERS_DIR).absolute()


def get_user(db: sqlite3.Connection, username: str) -> User | None:
    """Get a user from the database."""
    try:
        cursor = db.cursor()
        cursor.execute(
            "SELECT data FROM json_data WHERE username = ?", (str(username),)
        )
        row = cursor.fetchone()
        if row:
            user_data = json.loads(row[0])
            if "theme_preference" not in user_data:
                user_data["theme_preference"] = "system"
            return User(**user_data)
        else:
            logger.error(f"{username} not found")
            return None
    except Exception as e:
        logger.error(f"problem with get_user: {e}")
        return None


def auth_user(db: sqlite3.Connection, username: str, password: str) -> User | None:
    """Authenticate a user."""
    user = get_user(db, username)
    if user:
        password_hash = user.password
        if password_hash and check_password_hash(password_hash, password):
            logger.debug(f"returning {user}")
            return user
        else:
            logger.info("bad password")
            return None
    return None


def save_user(db: sqlite3.Connection, user: User, new_user: bool = False) -> bool:
    """Save a user to the database."""
    if not user.username:
        logger.warning("no username in user")
        return False
    username = user.username
    try:
        cursor = db.cursor()
        if new_user:
            cursor.execute(
                "INSERT INTO json_data (data, username) VALUES (?, ?)",
                (user.model_dump_json(), str(username)),
            )
            create_user_dir(username)
        else:
            cursor.execute(
                "UPDATE json_data SET data = ? WHERE username = ?",
                (user.model_dump_json(), str(username)),
            )
        db.commit()
        return True
    except Exception as e:
        logger.error(f"couldn't save {user}: {e}")
        return False


def delete_user(db: sqlite3.Connection, username: str) -> bool:
    """Delete a user from the database."""
    try:
        db.cursor().execute("DELETE FROM json_data WHERE username = ?", (username,))
        db.commit()
        # Securely delete the user's directory, preventing path traversal
        user_dir = get_users_dir() / secure_filename(username)
        try:
            user_dir.relative_to(get_users_dir())
        except ValueError:
            logger.warning("Security warning: Attempted path traversal in username")
            return False
        if user_dir.exists():
            shutil.rmtree(user_dir)
        logger.info(f"User {username} deleted successfully")
        return True
    except Exception as e:
        logger.error(f"Error deleting user {username}: {e}")
        return False


def create_user_dir(user: str) -> None:
    """Create a directory for a user."""
    user_dir = get_users_dir() / secure_filename(user)
    (user_dir / "apps").mkdir(parents=True, exist_ok=True)


def get_apps_list(user: str) -> list[AppMetadata]:
    """Get a list of apps for a user."""
    app_list: list[AppMetadata] = []
    if user == "system" or user == "":
        list_file = get_data_dir() / "system-apps.json"
        if not list_file.exists():
            logger.info("Generating apps.json file...")
            system_apps.update_system_repo(get_data_dir())
            logger.debug("apps.json file generated.")
        with list_file.open("r") as f:
            app_dicts = json.load(f)
            return [AppMetadata(**app_dict) for app_dict in app_dicts]

    dir = get_users_dir() / secure_filename(user) / "apps"
    # Ensure dir is within get_users_dir to prevent path traversal
    try:
        dir.relative_to(get_users_dir())
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in user")
        return []

    if not dir.exists():
        return []

    for file in dir.rglob("*.star"):
        app_name = file.stem
        # Get file modification time
        mod_time = datetime.fromtimestamp(file.stat().st_mtime)
        app_dict: dict[str, Any] = {
            "path": str(file),
            "id": app_name,
            "name": app_name,  # Ensure name is always set
            "date": mod_time.strftime("%Y-%m-%d %H:%M"),
        }
        preview = get_data_dir() / "apps" / f"{app_name}.webp"
        if preview.exists() and preview.stat().st_size > 0:
            app_dict["preview"] = str(preview.name)
        yaml_path = file.parent / "manifest.yaml"
        if yaml_path.exists():
            with yaml_path.open("r") as f:
                yaml_dict = yaml.safe_load(f)
                app_dict.update(yaml_dict)
        else:
            app_dict["summary"] = "Custom App"
        app_list.append(AppMetadata(**app_dict))
    return app_list


def get_app_details(
    user: str, field: Literal["id", "name"], value: str
) -> AppMetadata | None:
    """Get details for a specific app."""
    custom_apps = get_apps_list(user)
    for app in custom_apps:
        if getattr(app, field) == value:
            # we found it
            logger.debug(f"returning details for {app}")
            return app
        # Also check fileName if looking up by name (with or without .star extension)
        if field == "name":
            file_name = app.fileName or ""
            # Check both with and without .star extension
            file_name_base = file_name.removesuffix(".star")
            if file_name == value or file_name_base == value:
                logger.debug(f"returning details for {app} (matched by fileName)")
                return app
    # if we get here then the app is not in custom apps
    # so we need to look in the system-apps directory
    apps = get_apps_list("system")
    for app in apps:
        if getattr(app, field) == value:
            return app
        # Also check fileName if looking up by name (with or without .star extension)
        if field == "name":
            file_name = app.fileName or ""
            # Check both with and without .star extension
            file_name_base = file_name.removesuffix(".star")
            if file_name == value or file_name_base == value:
                logger.debug(f"returning details for {app} (matched by fileName)")
                return app
    return None


def get_app_details_by_name(user: str, name: str) -> AppMetadata | None:
    """Get app details by name."""
    return get_app_details(user, "name", name)


def get_app_details_by_id(user: str, id: str) -> AppMetadata | None:
    """Get app details by ID."""
    return get_app_details(user, "id", id)


def sanitize_url(url: str) -> str:
    """Sanitize a URL."""
    url = unquote(url)
    url = url.replace(" ", "_")
    for char in ["'", "\\"]:
        url = url.replace(char, "")
    return quote(url, safe="/:.?&=")


def allowed_file(filename: str) -> bool:
    """Check if a file is allowed."""
    return "." in filename and filename.rsplit(".", 1)[1].lower() in ["star"]


async def save_user_app(file: UploadFile, path: Path) -> bool:
    """Save a user's app."""
    filename = file.filename
    if not filename:
        return False
    filename = secure_filename(filename)
    # Ensure the path is within the user's apps directory to prevent path traversal
    user_apps_dir = path.resolve()
    file_path = (user_apps_dir / filename).resolve()
    try:
        file_path.relative_to(user_apps_dir)
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in filename")
        return False

    if file and allowed_file(filename):
        contents = await file.read()
        with open(file_path, "wb") as f:
            f.write(contents)
        return True
    else:
        return False


def delete_user_upload(user: User, filename: str) -> bool:
    """Delete a user's uploaded app."""
    user_apps_path = get_users_dir() / user.username / "apps"
    try:
        filename = secure_filename(filename)
        folder_name = Path(filename).stem
        folder_path = user_apps_path / folder_name
        file_path = folder_path / filename

        # Ensure paths are within get_users_dir to prevent path traversal
        try:
            user_apps_path.relative_to(get_users_dir())
            file_path.relative_to(user_apps_path)
            folder_path.relative_to(user_apps_path)
        except ValueError:
            logger.warning("Security warning: Attempted path traversal")
            return False

        # Only delete if folder_path exists and is a directory
        if folder_path.exists() and folder_path.is_dir():
            shutil.rmtree(folder_path)
        return True
    except OSError as e:
        logger.error(f"couldn't delete file: {e}")
        return False


def get_all_users(db: sqlite3.Connection) -> list[User]:
    """Get all users from the database."""
    cursor = db.cursor()
    cursor.execute("SELECT data FROM json_data")
    return [User(**json.loads(row[0])) for row in cursor.fetchall()]


def has_users(db: sqlite3.Connection) -> bool:
    """Check if any users exist in the database."""
    cursor = db.cursor()
    cursor.execute("SELECT 1 FROM json_data LIMIT 1")
    return cursor.fetchone() is not None


def get_is_app_schedule_active(app: App, device: Device) -> bool:
    """Check if an app's schedule is active."""
    current_time = datetime.now(get_device_timezone(device))
    return get_is_app_schedule_active_at_time(app, current_time)


def get_is_app_schedule_active_at_time(app: App, current_time: datetime) -> bool:
    """Check if app should be active at the given time using either legacy or new recurrence system."""
    # Check time range first
    start_time = app.start_time or datetime.strptime("00:00", "%H:%M").time()
    end_time = app.end_time or datetime.strptime("23:59", "%H:%M").time()

    current_time_only = current_time.replace(second=0, microsecond=0).time()
    if start_time > end_time:
        in_time_range = current_time_only >= start_time or current_time_only <= end_time
    else:
        in_time_range = start_time <= current_time_only <= end_time

    if not in_time_range:
        return False

    # Use custom recurrence system only if explicitly enabled
    if app.use_custom_recurrence and app.recurrence_type:
        return _is_recurrence_active_at_time(app, current_time)
    else:
        # Default to legacy daily schedule system
        current_day = current_time.strftime("%A").lower()
        active_days: list[Weekday] = app.days
        if not active_days:
            active_days = list(Weekday)  # All days active by default

        active_day_values = [day.value for day in active_days]
        return current_day in active_day_values


def _is_recurrence_active_at_time(app: App, current_time: datetime) -> bool:
    """Check if app recurrence pattern matches the current time."""
    recurrence_type = app.recurrence_type
    recurrence_interval = app.recurrence_interval
    recurrence_pattern = app.recurrence_pattern
    recurrence_start_date = app.recurrence_start_date
    recurrence_end_date = app.recurrence_end_date

    # Parse start date
    if not recurrence_start_date:
        # Default to a reasonable start date if not specified
        recurrence_start_date = datetime(2025, 1, 1).date()

    # Check end date
    if recurrence_end_date:
        if current_time.date() > recurrence_end_date:
            return False

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
        if (
            recurrence_pattern
            and recurrence_pattern.weekdays
            and isinstance(recurrence_pattern.weekdays, list)
        ):
            weekdays = recurrence_pattern.weekdays
        else:
            weekdays = list(Weekday)  # All days if none specified

        current_weekday = current_time.strftime("%A").lower()
        return current_weekday in [day.value for day in weekdays]

    elif recurrence_type == "monthly":
        # Every X months on specified day or weekday pattern
        months_since_start = _months_between_dates(recurrence_start_date, current_date)
        if months_since_start < 0 or months_since_start % recurrence_interval != 0:
            return False

        if recurrence_pattern:
            if recurrence_pattern.day_of_month is not None:
                # Specific day of month (e.g., 1st, 15th)
                return current_date.day == recurrence_pattern.day_of_month
            elif recurrence_pattern.day_of_week is not None:
                # Specific weekday pattern (e.g., "first_monday", "last_friday")
                return _matches_monthly_weekday_pattern(
                    current_date, recurrence_pattern.day_of_week
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
            logger.warning(
                f"Invalid monthly weekday pattern '{pattern}' for date {target_date}: {e}"
            )
        except RuntimeError:
            # Outside application context, skip logging
            pass
        return False


def get_device_by_name(user: User, name: str) -> Device | None:
    """Get a device by name."""
    for device in user.devices.values():
        if device.name == name:
            return device
    return None


def get_device_webp_dir(device_id: str, create: bool = True) -> Path:
    """Get the WebP directory for a device."""
    path = get_data_dir() / "webp" / secure_filename(device_id)
    if not path.exists() and create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def get_device_by_id(db: sqlite3.Connection, device_id: str) -> Device | None:
    """Get a device by ID."""
    for user in get_all_users(db):
        device = user.devices.get(device_id)
        if device:
            return device
    return None


def get_user_by_device_id(db: sqlite3.Connection, device_id: str) -> User | None:
    """Get a user by device ID."""
    for user in get_all_users(db):
        device = user.devices.get(device_id)
        if device:
            return user
    return None


def get_firmware_version() -> str | None:
    """Get the current firmware version."""
    version_file = get_data_dir() / "firmware" / "firmware_version.txt"
    try:
        if version_file.exists():
            with version_file.open("r") as f:
                return f.read().strip()
    except Exception as e:
        logger.error(f"Error reading firmware version: {e}")
    return None


def get_user_by_api_key(db: sqlite3.Connection, api_key: str) -> User | None:
    """Get a user by API key."""
    for user in get_all_users(db):
        if user.api_key == api_key:
            return user
    return None


def get_pushed_app(user: User, device_id: str, installation_id: str) -> dict[str, Any]:
    """Get a pushed app."""
    device = user.devices.get(device_id)
    if not device:
        return {}

    apps = device.apps
    if installation_id in apps:
        return apps[installation_id].model_dump()

    app = {
        "id": installation_id,
        "path": f"pushed/{installation_id}",
        "iname": installation_id,
        "name": "pushed",
        "uinterval": 10,
        "display_time": 0,
        "notes": "",
        "enabled": True,
        "pushed": True,
        "order": len(apps),
    }
    return app


def add_pushed_app(
    db: sqlite3.Connection, device_id: str, installation_id: str
) -> None:
    """Add a pushed app to a device."""
    user = get_user_by_device_id(db, device_id)
    if not user:
        raise ValueError("User not found")

    device = user.devices.get(device_id)
    if not device:
        raise ValueError("Device not found")

    app_data = get_pushed_app(user, device_id, installation_id)
    if not app_data:
        return

    app = App(**app_data)
    device.apps[installation_id] = app
    save_user(db, user)


def save_app(db: sqlite3.Connection, device_id: str, app: App) -> bool:
    """Save an app."""
    try:
        user = get_user_by_device_id(db, device_id)
        if not user:
            return False
        if not app.iname:
            return True
        device = user.devices.get(device_id)
        if not device:
            return False
        device.apps[app.iname] = app
        save_user(db, user)
        return True
    except Exception:
        return False


def save_render_messages(
    db: sqlite3.Connection, device: Device, app: App, messages: list[str]
) -> None:
    """Save render messages from pixlet."""
    app.render_messages = messages
    if not save_app(db, device.id, app):
        logger.error("Error saving render messages: Failed to save app.")


def vacuum(db: sqlite3.Connection) -> None:
    page_count_row = db.execute("PRAGMA page_count").fetchone()
    freelist_count_row = db.execute("PRAGMA freelist_count").fetchone()

    if page_count_row and freelist_count_row:
        page_count = page_count_row[0]
        freelist_count = freelist_count_row[0]

        # Avoid division by zero for empty DB
        if page_count > 0:
            # Run vacuum if more than 20% of pages are free, and there are at least 100 free pages.
            fragmentation_ratio = freelist_count / page_count
            if freelist_count > 100 and fragmentation_ratio > 0.2:
                logger.info(
                    f"Database is fragmented ({freelist_count} of {page_count} pages are free, "
                    f"{fragmentation_ratio:.1%}). Vacuuming..."
                )
                db.execute("VACUUM")
                logger.info("Database vacuum complete.")
