import json
import os
import shutil
import sqlite3
import subprocess
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Dict, List, Optional, Union
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from flask import current_app, g
from werkzeug.security import check_password_hash, generate_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server.models.app import App
from tronbyt_server.models.device import Device
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


def get_night_mode_is_active(device: Device) -> bool:
    if not device.get("night_mode_enabled", False):
        return False
    if "timezone" in device and device["timezone"] != "":
        if isinstance(device["timezone"], int):
            # Legacy case: timezone is an int representing offset in hours
            current_hour = (datetime.now(timezone.utc).hour + device["timezone"]) % 24
        else:
            # configured, adjust current hour to set device timezone
            current_hour = datetime.now(ZoneInfo(device["timezone"])).hour
    else:
        current_hour = datetime.now().hour
    # current_app.logger.debug(f"current_hour:{current_hour} -- ",end="")
    if device.get("night_start", -1) > -1:
        start_hour = device["night_start"]
        end_hour = device.get("night_end", 6)  # default to 6 am if not set
        if start_hour <= end_hour:  # Normal case (e.g., 9 to 17)
            if start_hour <= current_hour <= end_hour:
                current_app.logger.debug("nightmode active")
                return True
        else:  # Wrapped case (e.g., 22 to 6 - overnight)
            if current_hour >= start_hour or current_hour <= end_hour:
                current_app.logger.debug("nightmode active")
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


# Ensure all apps have an "order" attribute
# Earlier releases did not have this attribute,
# so we need to ensure it exists to allow reordering of the app list.
# Eventually, this function should be deleted.
def ensure_app_order(user: User) -> None:
    modified = False
    for device in user.get("devices", {}).values():
        apps = device.get("apps", {})
        for idx, app in enumerate(apps.values()):
            if "order" not in app:
                app["order"] = idx
                modified = True

    if modified:
        save_user(user)


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
            ensure_app_order(user)
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


def get_apps_list(user: str) -> List[Dict[str, Any]]:
    app_list: List[Dict[str, Any]] = list()
    # test for directory named dir and if not exist create it
    if user == "system" or user == "":
        list_file = Path("system-apps.json")
        if not list_file.exists():
            current_app.logger.info("Generating apps.json file...")
            subprocess.run(["python3", "clone_system_apps_repo.py"])
            current_app.logger.debug("apps.json file generated.")

        with list_file.open("r") as f:
            app_list = json.load(f)
            return app_list

    dir = get_users_dir() / user / "apps"
    if dir.exists():
        for file in dir.rglob("*.star"):
            app_name = file.stem
            app_dict = {
                "path": str(file),
                "name": app_name,
                "image_url": app_name + ".gif",
            }
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


def get_app_details(user: str, name: str) -> Dict[str, Any]:
    # first look for the app name in the custom apps
    custom_apps = get_apps_list(user)
    current_app.logger.debug(f"{user} {name}")
    for app in custom_apps:
        current_app.logger.debug(app)
        if app["name"] == name:
            # we found it
            return app
    # if we get here then the app is not in custom apps
    # so we need to look in the system-apps directory
    apps = get_apps_list("system")
    for app in apps:
        if app["name"] == name:
            return app
    return {}


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


def save_user_app(file: Any, path: Path) -> bool:
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


def get_user_render_port(username: str) -> Optional[int]:
    base_port = int(current_app.config.get("PIXLET_RENDER_PORT1", 5100))
    users = get_all_users()
    for i in range(len(users)):
        if users[i]["username"] == username:
            # current_app.logger.debug(f"got port {i} for {username}")
            return base_port + i
    return None


def get_is_app_schedule_active(app: App, tz_str: Optional[str]) -> bool:
    current_time = datetime.now()
    if tz_str:
        try:
            current_time = datetime.now(ZoneInfo(tz_str))
        except Exception as e:
            current_app.logger.warning(f"Error converting timezone: {e}")

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
    label: str, url: str, ap: str, pw: str, gen2: bool, swap_colors: bool
) -> Dict[str, Union[str, int]]:
    if gen2:
        file_path = Path("firmware/gen2.bin")
        new_path = Path(f"firmware/gen2_{label}.bin")
    elif swap_colors:
        file_path = Path("firmware/gen1_swap.bin")
        new_path = Path(f"firmware/gen1_swap_{label}.bin")
    else:
        file_path = Path("firmware/gen1.bin")
        new_path = Path(f"firmware/gen1_{label}.bin")

    if not file_path.exists():
        return {"error": f"Firmware file {file_path} not found."}

    shutil.copy(file_path, new_path)

    dict = {
        "XplaceholderWIFISSID________________________________": ap,
        "XplaceholderWIFIPASSWORD____________________________": pw,
        "XplaceholderREMOTEURL_________________________________________________________________________________________": url,
    }
    bytes_written = None
    with new_path.open("r+b") as f:
        content = f.read()

        for old_string, new_string in dict.items():
            # Ensure the new string is not longer than the original
            if len(new_string) > len(old_string):
                return {
                    "error": "Replacement string cannot be longer than the original string."
                }

            # Find the position of the old string
            position = content.find(old_string.encode("ascii") + b"\x00")
            if position == -1:
                return {"error": f"String '{old_string}' not found in the binary."}

            # Create the new string, null-terminated, and padded to match the original length
            padded_new_string = new_string + "\x00"
            # Add padding if needed
            padded_new_string = padded_new_string.ljust(len(old_string) + 1, "\x00")

            f.seek(position)
            bytes_written = f.write(padded_new_string.encode("ascii"))
    if bytes_written:
        # run the correct checksum/hash script
        result = subprocess.run(
            [
                "python3",
                "firmware/correct_firmware_esptool.py",
                f"{new_path}",
            ],
            capture_output=True,
            text=True,
        )
        current_app.logger.debug(result.stdout)
        current_app.logger.debug(result.stderr)
        return {"file_path": str(new_path.resolve())}
    else:
        return {"error": "no bytes written"}


def add_pushed_app(device_id: str, path: Path) -> None:
    user = get_user_by_device_id(device_id)
    if user is None:
        return
    installation_id = path.stem
    apps = user["devices"][device_id].setdefault("apps", {})
    if installation_id in apps:
        # already in there
        return
    app = App(
        iname=installation_id,
        name="pushed",
        uinterval=10,
        display_time=0,
        notes="",
        enabled=True,
        pushed=1,
        order=len(apps),
    )
    apps[installation_id] = app
    save_user(user)
