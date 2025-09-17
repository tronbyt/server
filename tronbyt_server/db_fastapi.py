import json
import logging
import secrets
import shutil
import sqlite3
import string
from datetime import datetime
from pathlib import Path
from typing import List, Literal, Optional, Union
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from tzlocal import get_localzone, get_localzone_name
from werkzeug.datastructures import FileStorage
from werkzeug.security import check_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server import system_apps_fastapi as system_apps
from tronbyt_server.firmware import correct_firmware_esptool
from tronbyt_server.models.app import App, AppMetadata
from tronbyt_server.models.device import (
    Device,
    validate_timezone,
)
from tronbyt_server.models.user import User

logging.basicConfig(filename='db.log', level=logging.INFO)


def create_tables(conn: sqlite3.Connection):
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


def get_db_connection() -> sqlite3.Connection:
    conn = sqlite3.connect("users/usersdb.sqlite", autocommit=True)
    create_tables(conn)
    return conn


def init_db(logger) -> None:
    (get_users_dir() / "admin" / "configs").mkdir(parents=True, exist_ok=True)
    conn = get_db_connection()
    cursor = conn.cursor()
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

        # Perform migration tasks here
        if schema_version < 1:
            migrate_app_configs(logger)
            migrate_app_paths(logger)
            migrate_brightness_to_percent(logger)
        if schema_version < 2:
            migrate_user_api_keys(logger)

        cursor.execute(
            "UPDATE meta SET schema_version = ? WHERE schema_version = ?",
            (get_current_schema_version(), schema_version),
        )
        logger.info(
            f"Schema version {schema_version} migrated to {get_current_schema_version()}"
        )


def get_current_schema_version() -> int:
    return 2


def migrate_app_configs(logger) -> None:
    users = get_all_users(logger)
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
            save_user(logger, user)


def migrate_app_paths(logger) -> None:
    users = get_all_users(logger)
    need_save = False
    for user in users:
        for device in user.get("devices", {}).values():
            for app in device.get("apps", {}).values():
                if "path" not in app or app.get("path", "").startswith(
                    "/"
                ):  # regenerate any absolute paths
                    app["path"] = get_app_details_by_name(
                        logger, user["username"], app["name"]
                    ).get("path") or str(
                        Path("system-apps")
                        / "apps"
                        / app["name"].replace("_", "")
                        / f"{app['name']}.star"
                    )
                    need_save = True
        if need_save:
            save_user(logger, user)


def migrate_brightness_to_percent(logger) -> None:
    users = get_all_users(logger)
    logger.info("Migrating brightness values to percentage-based storage")

    for user in users:
        need_save = False
        for device in user.get("devices", {}).values():
            if "brightness" in device and device["brightness"] <= 5:
                old_value = device["brightness"]
                device["brightness"] = ui_scale_to_percent(old_value)
                need_save = True
                logger.debug(
                    f"Converted brightness from {old_value} to {device['brightness']}%"
                )

            if "night_brightness" in device and device["night_brightness"] <= 5:
                old_value = device["night_brightness"]
                device["night_brightness"] = ui_scale_to_percent(old_value)
                need_save = True
                logger.debug(
                    f"Converted night_brightness from {old_value} to {device['night_brightness']}%"
                )

        if need_save:
            logger.info(
                f"Migrating brightness for user: {user['username']}"
            )
            save_user(logger, user)


def migrate_user_api_keys(logger) -> None:
    users = get_all_users(logger)
    logger.info("Migrating users to add API keys")

    for user in users:
        if not user.get("api_key"):
            api_key = "".join(
                secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
            )
            user["api_key"] = api_key
            logger.info(f"Generated API key for user: {user['username']}")
            save_user(logger, user)


def delete_device_dirs(logger, device_id: str) -> None:
    dir_to_delete = get_data_dir() / "webp" / device_id
    try:
        shutil.rmtree(dir_to_delete)
        logger.debug(f"Successfully deleted directory: {dir_to_delete}")
    except FileNotFoundError:
        logger.error(f"Directory not found: {dir_to_delete}")
    except Exception as e:
        logger.error(f"Error deleting directory {dir_to_delete}: {str(e)}")


def get_last_app_index(logger, device_id: str) -> Optional[int]:
    device = get_device_by_id(logger, device_id)
    if device is None:
        return None
    return device.get("last_app_index", 0)


def save_last_app_index(logger, device_id: str, index: int) -> None:
    user = get_user_by_device_id(logger, device_id)
    if user is None:
        return
    device = user["devices"].get(device_id)
    if device is None:
        return
    device["last_app_index"] = index
    save_user(logger, user)


def get_device_timezone(device: Device) -> ZoneInfo:
    if location := device.get("location"):
        if tz := location.get("timezone"):
            if zi := validate_timezone(tz):
                return zi

    if tz := device.get("timezone"):
        if zi := validate_timezone(tz):
            return zi
        elif isinstance(tz, int):
            hours_offset = int(tz)
            sign = "+" if hours_offset >= 0 else "-"
            return ZoneInfo(f"Etc/GMT{sign}{abs(hours_offset)}")

    return get_localzone()


def get_device_timezone_str(device: Device) -> str:
    zone_info = get_device_timezone(device)
    return zone_info.key or get_localzone_name()


def get_night_mode_is_active(logger, device: Device) -> bool:
    if not device.get("night_mode_enabled", False):
        return False

    current_hour = datetime.now(get_device_timezone(device)).hour

    start_hour = device.get("night_start", -1)
    if start_hour > -1:
        end_hour = device.get("night_end", 6)
        if start_hour <= end_hour:
            if start_hour <= current_hour < end_hour:
                logger.debug("Night mode active")
                return True
        else:
            if current_hour >= start_hour or current_hour < end_hour:
                logger.debug("Night mode active")
                return True

    return False


def get_device_brightness_8bit(logger, device: Device) -> int:
    if get_night_mode_is_active(logger, device):
        return device.get("night_brightness", 1)
    else:
        return device.get("brightness", 50)


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


def ui_scale_to_percent(scale_value: int) -> int:
    lookup = {
        0: 0,
        1: 3,
        2: 12,
        3: 20,
        4: 35,
        5: 100,
    }
    if scale_value in lookup:
        return lookup[scale_value]
    return 50


def brightness_map_8bit_to_levels(brightness: int) -> int:
    return percent_to_ui_scale(brightness)


def get_data_dir() -> Path:
    return Path("data").absolute()


def get_users_dir() -> Path:
    return Path("users").absolute()


def get_user(logger, username: str) -> Optional[User]:
    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        cursor.execute(
            "SELECT data FROM json_data WHERE username = ?", (str(username),)
        )
        row = cursor.fetchone()
        if row:
            user: User = json.loads(row[0])
            if "theme_preference" not in user:
                user["theme_preference"] = "system"
            return user
        else:
            logger.error(f"{username} not found")
            return None
    except Exception as e:
        logger.error("problem with get_user" + str(e))
        return None


def auth_user(logger, username: str, password: str) -> Optional[Union[User, bool]]:
    user = get_user(logger, username)
    logging.info(f"get_user result in auth_user for '{username}': {user}")
    if user:
        logging.info(f"Password hash from DB: {user.get('password')}")
        logging.info(f"Password to check: {password}")
        password_hash = user.get("password")
        if password_hash:
            logging.info(f"Hash check result: {check_password_hash(password_hash, password)}")
        if password_hash and check_password_hash(password_hash, password):
            logger.debug(f"returning {user}")
            return user
        else:
            logger.info("bad password")
            return False
    return None


def save_user(logger, user: User, new_user: bool = False) -> bool:
    if "username" not in user:
        logger.warning("no username in user")
        return False
    username = user["username"]

    # If user is a Pydantic model, convert it to a dict for serialization
    if hasattr(user, 'model_dump_json'):
        user_data = user.model_dump_json()
    elif hasattr(user, 'model_dump'):
        user_data = json.dumps(user.model_dump())
    elif isinstance(user, dict):
        # A bit of a hack to serialize App models within a dict
        user_data = json.dumps(user, default=lambda o: o.model_dump() if hasattr(o, 'model_dump') else o)
    else:
        user_data = json.dumps(user)

    try:
        conn = get_db_connection()
        cursor = conn.cursor()
        if new_user:
            cursor.execute(
                "INSERT INTO json_data (data, username) VALUES (?, ?)",
                (user_data, str(username)),
            )
            create_user_dir(username)
        else:
            cursor.execute(
                "UPDATE json_data SET data = ? WHERE username = ?",
                (user_data, str(username)),
            )
        conn.commit()
        return True
    except Exception as e:
        logger.error("couldn't save {} : {}".format(user, str(e)))
        return False


def delete_user(logger, username: str) -> bool:
    try:
        conn = get_db_connection()
        conn.cursor().execute("DELETE FROM json_data WHERE username = ?", (username,))
        conn.commit()
        user_dir = get_users_dir() / username
        if user_dir.exists():
            shutil.rmtree(user_dir)
        logger.info(f"User {username} deleted successfully")
        return True
    except Exception as e:
        logger.error(f"Error deleting user {username}: {e}")
        return False


def create_user_dir(user: str) -> None:
    user_dir = get_users_dir() / secure_filename(user)
    (user_dir / "configs").mkdir(parents=True, exist_ok=True)
    (user_dir / "apps").mkdir(parents=True, exist_ok=True)


def get_apps_list(logger, user: str) -> List[AppMetadata]:
    app_list: List[AppMetadata] = list()
    if user == "system" or user == "":
        list_file = get_data_dir() / "system-apps.json"
        if not list_file.exists():
            logger.info("Generating apps.json file...")
            system_apps.update_system_repo(logger, get_data_dir())
            logger.debug("apps.json file generated.")
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
            app_list.append(app_dict)
        return app_list
    else:
        return []


def get_app_details(
    logger, user: str, field: Literal["id", "name"], value: str
) -> AppMetadata:
    custom_apps = get_apps_list(logger, user)
    for app in custom_apps:
        if app[field] == value:
            logger.debug(f"returning details for {app}")
            return app
    apps = get_apps_list(logger, "system")
    for app in apps:
        if app[field] == value:
            return app
    return {}


def get_app_details_by_name(logger, user: str, name: str) -> AppMetadata:
    return get_app_details(logger, user, "name", name)


def get_app_details_by_id(logger, user: str, id: str) -> AppMetadata:
    return get_app_details(logger, user, "id", id)


def sanitize_url(url: str) -> str:
    url = unquote(url)
    url = url.replace(" ", "_")
    for char in ["'", "\\"]:
        url = url.replace(char, "")
    url = quote(url, safe="/:.?&=")
    return url


def allowed_file(filename: str) -> bool:
    return filename.lower().endswith(".star")


def save_user_app(file, path: Path) -> bool:
    filename = file.filename
    if not filename:
        return False
    filename = secure_filename(filename)
    if file and allowed_file(filename):
        with open(path / filename, "wb") as f:
            f.write(file.file.read())
        return True
    else:
        return False


def delete_user_upload(logger, user: User, filename: str) -> bool:
    user_apps_path = get_users_dir() / user["username"] / "apps"
    try:
        filename = secure_filename(filename)
        folder_name = Path(filename).stem
        file_path = user_apps_path / folder_name / filename
        folder_path = user_apps_path / folder_name
        if not str(file_path).startswith(str(user_apps_path)):
            logger.warning("Security warning: Attempted path traversal")
            return False
        if folder_path.exists():
            shutil.rmtree(folder_path)
        return True
    except OSError as e:
        logger.error(f"couldn't delete file: {e}")
        return False


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


def delete_user_upload(logger, user: User, filename: str) -> bool:
    user_apps_path = get_users_dir() / user["username"] / "apps"
    try:
        filename = secure_filename(filename)
        folder_name = Path(filename).stem
        file_path = user_apps_path / folder_name / filename
        folder_path = user_apps_path / folder_name
        if not str(file_path).startswith(str(user_apps_path)):
            logger.warning("Security warning: Attempted path traversal")
            return False
        if folder_path.exists():
            shutil.rmtree(folder_path)
        return True
    except OSError as e:
        logger.error(f"couldn't delete file: {e}")
        return False


def get_all_users(logger) -> List[User]:
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT data FROM json_data")
    return [json.loads(row[0]) for row in cursor.fetchall()]


def has_users() -> bool:
    conn = get_db_connection()
    cursor = conn.cursor()
    cursor.execute("SELECT 1 FROM json_data LIMIT 1")
    return cursor.fetchone() is not None


def get_is_app_schedule_active(app: App, device: Device) -> bool:
    current_time = datetime.now(get_device_timezone(device))
    return get_is_app_schedule_active_at_time(app, current_time)


def get_is_app_schedule_active_at_time(app: App, current_time: datetime) -> bool:
    current_day = current_time.strftime("%A").lower()
    start_time = datetime.strptime(str(app.start_time or "00:00"), "%H:%M").time()
    end_time = datetime.strptime(str(app.end_time or "23:59"), "%H:%M").time()
    active_days = app.days or [
        "monday",
        "tuesday",
        "wednesday",
        "thursday",
        "friday",
        "saturday",
        "sunday",
    ]
    current_time_only = current_time.replace(second=0, microsecond=0).time()
    if start_time > end_time:
        in_time_range = current_time_only >= start_time or current_time_only <= end_time
    else:
        in_time_range = start_time <= current_time_only <= end_time
    return (
        in_time_range and isinstance(active_days, list) and current_day in active_days
    )


def get_device_by_name(logger, user: User, name: str) -> Optional[Device]:
    for device in user.get("devices", {}).values():
        if device.get("name") == name:
            return device
    return None


def get_device_webp_dir(device_id: str, create: bool = True) -> Path:
    path = get_data_dir() / "webp" / secure_filename(device_id)
    if not path.exists() and create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def get_device_by_id(logger, device_id: str) -> Optional[Device]:
    for user in get_all_users(logger):
        device = user.get("devices", {}).get(device_id)
        if device:
            return device
    return None


def get_user_by_device_id(logger, device_id: str) -> Optional[User]:
    for user in get_all_users(logger):
        device = user.get("devices", {}).get(device_id)
        if device:
            return user
    return None


def get_firmware_version(logger) -> Optional[str]:
    version_file = get_data_dir() / "firmware" / "firmware_version.txt"
    try:
        if version_file.exists():
            with version_file.open("r") as f:
                return f.read().strip()
    except Exception as e:
        logger.error(f"Error reading firmware version: {e}")
    return None


def get_user_by_api_key(logger, api_key: str) -> Optional[User]:
    for user in get_all_users(logger):
        if user.get("api_key") == api_key:
            return user
    return None


def generate_firmware(
    url: str, ap: str, pw: str, device_type: str, swap_colors: bool
) -> bytes:
    if device_type == "tidbyt_gen2":
        firmware_filename = "tidbyt-gen2.bin"
    elif device_type == "pixoticker":
        firmware_filename = "pixoticker.bin"
    elif device_type == "tronbyt_s3":
        firmware_filename = "tronbyt-S3.bin"
    elif device_type == "tronbyt_s3_wide":
        firmware_filename = "tronbyt-s3-wide.bin"
    elif swap_colors:
        firmware_filename = "tidbyt-gen1_swap.bin"
    else:
        firmware_filename = "tidbyt-gen1.bin"

    data_firmware_path = get_data_dir() / "firmware" / firmware_filename
    bundled_firmware_path = Path(__file__).parent / "firmware" / firmware_filename

    if data_firmware_path.exists():
        file_path = data_firmware_path
    elif bundled_firmware_path.exists():
        file_path = bundled_firmware_path
    else:
        raise ValueError(
            f"Firmware file {firmware_filename} not found in {data_firmware_path} or {bundled_firmware_path}."
        )

    dict = {
        "XplaceholderWIFISSID____________": ap,
        "XplaceholderWIFIPASSWORD________________________________________": pw,
        "XplaceholderREMOTEURL___________________________________________________________________________________________________________": url,
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


def get_pushed_app(logger, user: User, device_id: str, installation_id: str) -> App:
    apps = user["devices"][device_id].setdefault("apps", {})
    if installation_id in apps:
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


def add_pushed_app(logger, device_id: str, installation_id: str) -> None:
    user = get_user_by_device_id(logger, device_id)
    if not user:
        raise ValueError("User not found")

    app = get_pushed_app(logger, user, device_id, installation_id)
    apps = user["devices"][device_id].setdefault("apps", {})
    apps[installation_id] = app
    save_user(logger, user)


def save_app(logger, device_id: str, app: App) -> bool:
    try:
        user = get_user_by_device_id(logger, device_id)
        if not user:
            return False
        if not app["iname"]:
            return True
        user["devices"][device_id]["apps"][app["iname"]] = app
        save_user(logger, user)
        return True
    except Exception:
        return False


def save_render_messages(
    logger, device: Device, app: App, messages: List[str]
) -> None:
    app["render_messages"] = messages
    if not save_app(logger, device["id"], app):
        logger.error("Error saving render messages: Failed to save app.")
