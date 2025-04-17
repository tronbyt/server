import json
import os
import shutil
import sqlite3
from datetime import datetime
from pathlib import Path
from typing import List, Literal, Optional, Union
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from flask import current_app, g
from tzlocal import get_localzone_name
from werkzeug.datastructures import FileStorage
from werkzeug.security import check_password_hash, generate_password_hash
from werkzeug.utils import secure_filename

from firmware import correct_firmware_esptool
from tronbyt_server import system_apps
from tronbyt_server.models.app import App, AppMetadata
from tronbyt_server.models.device import (
    Device,
    validate_timezone,
)
from tronbyt_server.models.user import User


def init_db() -> None:
    Path("users/admin/configs").mkdir(parents=True, exist_ok=True)
    conn = get_db()
    cursor = conn.cursor()
    cursor.execute(
        """CREATE TABLE IF NOT EXISTS json_data (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        username TEXT NOT NULL UNIQUE,
        data TEXT NOT NULL
    )
    """
    )
    conn.commit()
    cursor.execute("SELECT * FROM json_data WHERE username='admin'")
    row = cursor.fetchone()

    if not row:  # If no row is found
        # Load the default JSON data
        default_json = {
            "username": "admin",
            "password": generate_password_hash("password"),
        }

        # Insert default JSON
        cursor.execute(
            "INSERT INTO json_data (data, username) VALUES (?, 'admin')",
            (json.dumps(default_json),),
        )
        conn.commit()
        current_app.logger.debug("Default JSON inserted for admin user")
    else:
        migrate_app_configs()
        migrate_app_paths()


def migrate_app_configs() -> None:
    users = get_all_users()
    users_dir = get_users_dir()
    need_save = False
    for user in users:
        for device in user.get("devices", {}).values():
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
                if "path" not in app:
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
    local_tz = get_localzone_name()
    return ZoneInfo(local_tz)


def get_device_timezone_str(device: Device) -> str:
    zone_info = get_device_timezone(device)
    return datetime.now(zone_info).tzname() or get_localzone_name()


def get_night_mode_is_active(device: Device) -> bool:
    if not device.get("night_mode_enabled", False):
        return False

    # get_device_timezone will always return a valid tz string
    current_hour = datetime.now(get_device_timezone(device)).hour

    # Determine if night mode is active
    if device.get("night_start", -1) > -1:
        start_hour = device["night_start"]
        end_hour = device.get("night_end", 6)  # default to 6 am if not set
        if start_hour <= end_hour:  # Normal case (e.g., 9 to 17)
            if start_hour <= current_hour <= end_hour:
                current_app.logger.debug("Night mode active")
                return True
        else:  # Wrapped case (e.g., 22 to 6 - overnight)
            if current_hour >= start_hour or current_hour <= end_hour:
                current_app.logger.debug("Night mode active")
                return True

    return False


# selectable range is 0 - 5, lowest actually visible value returned should be 3, so 0 = 0, 1 = 3, etc.
def get_device_brightness_8bit(device: Device) -> int:
    lookup = {0: 0, 1: 3, 2: 5, 3: 10, 4: 50, 5: 100}
    if get_night_mode_is_active(device):
        b = device.get("night_brightness", 1)
    else:
        b = device.get("brightness", 5)

    return lookup.get(b, 50)


# map from 8bit values to 0 - 5
def brightness_map_8bit_to_levels(brightness: int) -> int:
    if brightness == 0:
        return 0
    elif brightness < 4:
        return 1
    elif brightness < 6:
        return 2
    elif brightness < 11:
        return 3
    elif brightness < 51:
        return 4
    else:
        return 5


def get_users_dir() -> Path:
    # current_app.logger.debug(f"users dir : {current_app.config['USERS_DIR']}")
    return Path(current_app.config["USERS_DIR"])


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

        if current_app.config.get("PRODUCTION") == "0":
            # current_app.logger.debug("writing to json file for visibility")
            with open(
                get_users_dir()
                / secure_filename(username)
                / secure_filename(f"{username}_debug.json"),
                "w",
            ) as file:
                json_string = json.dumps(user, indent=4)
                if current_app.testing:
                    current_app.logger.debug(f"writing json of {user}")
                else:
                    json_string.replace(
                        user["username"], "DO NOT EDIT THIS FILE, FOR DEBUG ONLY"
                    )
                file.write(json_string)

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
        list_file = Path("system-apps.json")
        if not list_file.exists():
            current_app.logger.info("Generating apps.json file...")
            system_apps.update_system_repo()
            current_app.logger.debug("apps.json file generated.")

        with list_file.open("r") as f:
            app_list = json.load(f)
            return app_list

    dir = get_users_dir() / user / "apps"
    if dir.exists():
        for file in dir.rglob("*.star"):
            app_name = file.stem
            app_dict = AppMetadata(
                path=str(file),
                id=app_name,
                name=app_name,
            )
            preview = Path("tronbyt_server/static/apps") / f"{app_name}.webp"
            if preview.exists() and preview.stat().st_size > 0:
                app_dict["preview"] = str(preview.name)
            yaml_path = file.parent / "manifest.yaml"
            current_app.logger.debug(f"checking for manifest.yaml in {yaml_path}")
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
        current_app.logger.debug(app)
        if app[field] == value:
            # we found it
            return app
    # if we get here then the app is not in custom apps
    # so we need to look in the system-apps directory
    apps = get_apps_list("system")
    for app in apps:
        if app[field] == value:
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
    filename = secure_filename(file.filename)
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


def get_is_app_schedule_active(app: App, device: Device) -> bool:
    current_time = datetime.now(get_device_timezone(device))
    return get_is_app_schedule_active_at_time(app, current_time)


def get_is_app_schedule_active_at_time(app: App, current_time: datetime) -> bool:
    current_day = current_time.strftime("%A").lower()
    start_time = datetime.strptime(
        str(app.get("start_time") or "00:00"), "%H:%M"
    ).time()
    end_time = datetime.strptime(str(app.get("end_time") or "23:59"), "%H:%M").time()
    active_days = app.get(
        "days",
        ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"],
    )

    current_time_only = current_time.replace(second=0, microsecond=0).time()
    if start_time > end_time:
        in_time_range = current_time_only >= start_time or current_time_only <= end_time
    else:
        in_time_range = start_time <= current_time_only <= end_time

    return (
        in_time_range and isinstance(active_days, list) and current_day in active_days
    )


def get_device_by_name(user: User, name: str) -> Optional[Device]:
    for device in user.get("devices", {}).values():
        if device.get("name") == name:
            return device
    return None


def get_device_webp_dir(device_id: str, create: bool = True) -> Path:
    base = os.getcwd()
    path = Path(base) / "tronbyt_server" / "webp" / secure_filename(device_id)
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


def generate_firmware(
    url: str, ap: str, pw: str, device_type: str, swap_colors: bool
) -> bytes:
    if device_type == "tidbyt_gen2":
        file_path = Path("firmware/gen2.bin")
    elif device_type == "pixoticker":
        file_path = Path("firmware/pixoticker.bin")
    elif device_type == "tronbyt_s3":
        # For S3, we need a complete firmware image that includes bootloader
        file_path = Path("firmware/tronbyt_S3.bin")
    elif swap_colors:
        file_path = Path("firmware/gen1_swap.bin")
    else:
        file_path = Path("firmware/gen1.bin")

    if not file_path.exists():
        raise ValueError(f"Firmware file {file_path} not found.")

    dict = {
        "XplaceholderWIFISSID________________________________": ap,
        "XplaceholderWIFIPASSWORD____________________________": pw,
        "XplaceholderREMOTEURL_________________________________________________________________________________________": url,
    }
    with file_path.open("rb") as f:
        content = f.read()

    for old_string, new_string in dict.items():
        if len(new_string) > len(old_string):
            raise ValueError(
                "Replacement string cannot be longer than the original string."
            )
        position = content.find(old_string.encode("ascii") + b"\x00")
        if position == -1:
            raise ValueError(f"String '{old_string}' not found in the binary.")
        padded_new_string = new_string + "\x00"
        padded_new_string = padded_new_string.ljust(len(old_string) + 1, "\x00")
        content = (
            content[:position]
            + padded_new_string.encode("ascii")
            + content[position + len(old_string) + 1 :]
        )

    return correct_firmware_esptool.update_firmware_data(content, device_type)


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
        # user.get("devices",{}).get("apps",{})
        print(app)
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
