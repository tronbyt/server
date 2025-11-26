"Database utility functions for Tronbyt Server."

import json
import logging
import secrets
import shutil
import sqlite3
import string
from contextlib import contextmanager
from datetime import date, datetime, timedelta
from pathlib import Path
from typing import Any, Generator, Literal
from urllib.parse import quote, unquote
from zoneinfo import ZoneInfo

import yaml
from fastapi import UploadFile
from pydantic import ValidationError
from sqlmodel import Session
from tzlocal import get_localzone, get_localzone_name
from werkzeug.security import check_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server import system_apps
from tronbyt_server.config import get_settings
from tronbyt_server.db_models import operations as db_ops
from tronbyt_server.models import App, AppMetadata, Brightness, Device, User, Weekday

logger = logging.getLogger(__name__)


@contextmanager
def db_transaction(
    session: Session | sqlite3.Connection,
) -> Generator[Session | sqlite3.Cursor, None, None]:
    """A context manager for database transactions.

    For SQLModel Session: just yields the session (transactions handled automatically)
    For sqlite3.Connection: yields a cursor with manual commit/rollback
    """
    if isinstance(session, Session):
        # SQLModel sessions handle transactions automatically
        yield session
    else:
        # Legacy sqlite3 connection handling
        cursor = session.cursor()
        try:
            yield cursor
            session.commit()
        except sqlite3.Error:
            session.rollback()
            raise


def get_db() -> sqlite3.Connection:
    """Get a database connection."""
    return sqlite3.connect(
        get_settings().DB_FILE, check_same_thread=False, timeout=10
    )  # Set a 10-second timeout


def init_db(conn: sqlite3.Connection | None = None) -> None:
    """Initialize the database with SQLModel tables and run migrations."""
    from tronbyt_server.db_models import create_db_and_tables
    import sqlite3

    # Enable WAL mode for better concurrency
    if conn is None:
        conn = sqlite3.connect(get_settings().DB_FILE, check_same_thread=False)
        should_close = True
    else:
        should_close = False
    try:
        row = conn.execute("PRAGMA journal_mode=WAL").fetchone()
        if not row or row[0].lower() != "wal":
            logger.warning("Failed to enable WAL mode. Concurrency might be limited.")

        # Check if we have an old database with json_data table
        cursor = conn.cursor()
        cursor.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name='json_data'"
        )
        has_json_data = cursor.fetchone() is not None

        # Check if we have the new users table
        cursor.execute(
            "SELECT name FROM sqlite_master WHERE type='table' AND name='users'"
        )
        has_users_table = cursor.fetchone() is not None

        if has_json_data and not has_users_table:
            logger.warning(
                "\n" + "=" * 70 + "\n"
                "OLD DATABASE FORMAT DETECTED - Running automatic migration...\n"
                "\n"
                "Your database uses the old JSON format. Converting to SQLModel.\n"
                "This is a one-time operation that will:\n"
                "  1. Create new relational tables\n"
                "  2. Migrate all your data\n"
                "  3. Rename json_data to json_data_backup (keeps your old data safe)\n"
                + "="
                * 70
            )

            # Run the migration automatically
            try:
                from tronbyt_server.migrations import perform_json_to_sqlmodel_migration

                logger.info("Starting automatic migration...")
                success = perform_json_to_sqlmodel_migration(get_settings().DB_FILE)

                if not success:
                    logger.error("❌ Automatic migration failed!")
                    raise RuntimeError(
                        "Database migration failed. Please check the logs above."
                    )

                logger.info("✅ Automatic migration completed successfully!")
                logger.info("Your data has been migrated to SQLModel format.")

            except Exception as e:
                logger.error(f"Error during automatic migration: {e}")
                import traceback

                traceback.print_exc()
                raise RuntimeError("Database migration failed. See error above.")

        if has_json_data and has_users_table:
            logger.info(
                "Both old and new tables detected - migration was completed previously"
            )

    finally:
        if should_close:
            conn.close()

    # Create all SQLModel tables (in case they don't exist)
    create_db_and_tables()
    logger.info("SQLModel database tables initialized")

    # Run Alembic migrations automatically
    try:
        from alembic.config import Config
        from alembic import command
        from pathlib import Path

        # Get paths - handle both development and container environments
        project_root = Path(__file__).parent.parent
        alembic_ini_path = project_root / "alembic.ini"
        alembic_dir = project_root / "alembic"

        # Check if alembic.ini exists
        if not alembic_ini_path.exists():
            logger.warning(
                f"alembic.ini not found at {alembic_ini_path}, skipping migrations"
            )
            return

        # Create Alembic config
        alembic_cfg = Config(str(alembic_ini_path))

        # Override script_location to be absolute path
        alembic_cfg.set_main_option("script_location", str(alembic_dir))

        # Run migrations to head
        command.upgrade(alembic_cfg, "head")
        logger.info("Alembic migrations applied successfully")
    except ImportError:
        logger.warning("Alembic not installed, skipping migrations")
    except Exception as e:
        logger.warning(f"Could not run Alembic migrations (non-fatal): {e}")
        logger.info("Database tables were already initialized, continuing startup")
        # Don't fail startup if migrations fail - tables are already created
        pass


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
            if device.brightness.as_percent <= 5:
                old_value = device.brightness.as_percent
                device.brightness = Brightness.from_ui_scale(old_value)
                need_save = True
                logger.debug(
                    f"Converted brightness from {old_value} to {device.brightness.as_percent}%"
                )

            if device.night_brightness.as_percent <= 5:
                old_value = device.night_brightness.as_percent
                device.night_brightness = Brightness.from_ui_scale(old_value)
                need_save = True
                logger.debug(
                    f"Converted night_brightness from {old_value} to {device.night_brightness.as_percent}%"
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


def migrate_user_api_keys(session: Session) -> None:
    """Generate API keys for users who don't have them."""
    users = get_all_users(session)
    logger.info("Migrating users to add API keys")

    for user in users:
        if not user.api_key:
            user.api_key = "".join(
                secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
            )
            logger.info(f"Generated API key for user: {user.username}")
            save_user(session, user)


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
            return True
    else:  # Wrapped case (e.g., 22:00 to 7:00 - overnight)
        if current_time_minutes >= start_minutes or current_time_minutes < end_minutes:
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
        return device.night_brightness.as_percent
    # If we're in dim mode (but not night mode), use dim_brightness
    elif get_dim_mode_is_active(device):
        return (
            device.dim_brightness.as_percent
            if device.dim_brightness is not None
            else device.brightness.as_percent
        )
    else:
        return device.brightness.as_percent


def get_data_dir() -> Path:
    """Get the data directory."""
    return Path(get_settings().DATA_DIR).absolute()


def get_users_dir() -> Path:
    """Get the users directory."""
    return Path(get_settings().USERS_DIR).absolute()


def get_user(session: Session, username: str) -> User | None:
    """Get a user from the database."""
    try:
        user_db = db_ops.get_user_by_username(session, username)
        if user_db:
            return db_ops.load_user_full(session, user_db)
        else:
            logger.error(f"{username} not found")
            return None
    except Exception as e:
        logger.error(f"problem with get_user: {e}")
        return None


def auth_user(session: Session, username: str, password: str) -> User | None:
    """Authenticate a user."""
    user = get_user(session, username)
    if user:
        password_hash = user.password
        if password_hash and check_password_hash(password_hash, password):
            logger.debug(f"returning {user}")
            return user
        else:
            logger.info("bad password")
            return None
    return None


def save_user(session: Session, user: User, new_user: bool = False) -> bool:
    """Save a user to the database."""
    if not user.username:
        logger.warning("no username in user")
        return False
    try:
        db_ops.save_user_full(session, user, new_user=new_user)
        if new_user:
            create_user_dir(user.username)
        return True
    except Exception as e:
        logger.error(f"couldn't save {user}: {e}")
        return False


def delete_user(session: Session, username: str) -> bool:
    """Delete a user from the database."""
    try:
        if not db_ops.delete_user(session, username):
            return False

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
            apps: list[AppMetadata] = []
            for app in app_dicts:
                try:
                    apps.append(AppMetadata.model_validate(app))
                except ValidationError as e:
                    logger.error(
                        "AppMetadata validation failed for system app '%s': %s",
                        app.get("name", "unknown"),
                        e,
                    )
            return apps

    dir = get_users_dir() / secure_filename(user) / "apps"
    # Ensure dir is within get_users_dir to prevent path traversal
    try:
        dir.relative_to(get_users_dir())
    except ValueError:
        logger.warning("Security warning: Attempted path traversal in user")
        return []

    if not dir.exists():
        return []

    app_files: dict[str, Path] = {}
    # Prioritize .star files
    for file in dir.rglob("*.star"):
        app_files[file.stem] = file
    # Add .webp files only if a .star with the same name doesn't exist
    for file in dir.rglob("*.webp"):
        if file.stem not in app_files:
            app_files[file.stem] = file

    for file in sorted(app_files.values(), key=lambda f: f.name):
        app_name = file.stem
        # Get file modification time
        mod_time = datetime.fromtimestamp(file.stat().st_mtime)
        app_dict: dict[str, Any] = {
            "path": str(file),
            "id": app_name,
            "name": app_name,  # Ensure name is always set
            "date": mod_time.strftime("%Y-%m-%d %H:%M"),
        }

        if file.suffix.lower() == ".star":
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
        elif file.suffix.lower() == ".webp":
            app_dict["summary"] = "WebP Image"
            app_dict["preview"] = f"{app_name}.webp"

        try:
            app_list.append(AppMetadata.model_validate(app_dict))
        except ValidationError as e:
            logger.error(
                "AppMetadata validation failed for user app %s: %s", app_name, e
            )
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
    return "." in filename and filename.rsplit(".", 1)[1].lower() in ["star", "webp"]


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


def get_all_users(session: Session) -> list[User]:
    """Get all users from the database."""
    users_db = db_ops.get_all_users_db(session)
    users: list[User] = []
    for user_db in users_db:
        try:
            user = db_ops.load_user_full(session, user_db)
            users.append(user)
        except Exception as e:
            logger.error(f"Error loading user '{user_db.username}': {e}")
    return users


def has_users(session: Session) -> bool:
    """Check if any users exist in the database."""
    return db_ops.has_users(session)


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
        if recurrence_pattern and recurrence_pattern.weekdays:
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


def update_device_field(
    session: Session | sqlite3.Cursor,
    username: str,
    device_id: str,
    field: str,
    value: Any,
) -> None:
    """
    Update a single field for a device.
    Works with both SQLModel Session and legacy sqlite3 Cursor.
    """
    if isinstance(session, Session):
        # SQLModel approach
        device_db = db_ops.get_device_by_id(session, device_id)
        if device_db:
            # Convert string datetime to datetime object if needed
            if field == "last_seen" and isinstance(value, str):
                value = datetime.fromisoformat(value)

            # Handle nested fields like "info.protocol_type"
            if "." in field:
                parts = field.split(".", 1)
                if parts[0] == "info" and isinstance(device_db.info, dict):
                    device_db.info[parts[1]] = value
                else:
                    setattr(device_db, field, value)
            else:
                setattr(device_db, field, value)
            session.add(device_db)
            session.commit()
            logger.debug(f"Updated {field} for device {device_id}")
    else:
        # Legacy JSON approach
        cursor = session
        if field.startswith("apps"):
            raise ValueError("Use save_app or update_app_field to modify apps.")
        device_path = f"$.devices.{device_id}"
        path = f"{device_path}.{field}"

        cursor.execute(
            """
            UPDATE json_data
            SET data = json_set(data, ?, ?)
            WHERE username = ? AND json_type(data, ?) IS NOT NULL
            """,
            (path, value, username, device_path),
        )
        logger.debug(f"Queued update for {field} for device {device_id}")


def update_app_field(
    session: Session | sqlite3.Cursor,
    username: str,
    device_id: str,
    iname: str,
    field: str,
    value: Any,
) -> None:
    """
    Update a single field for an app.
    Works with both SQLModel Session and legacy sqlite3 Cursor.
    """
    if isinstance(session, Session):
        # SQLModel approach
        app_db = db_ops.get_app_by_device_and_iname(session, device_id, iname)
        if app_db:
            setattr(app_db, field, value)
            session.add(app_db)
            session.commit()
            logger.debug(f"Updated {field} for app {iname} on device {device_id}")
    else:
        # Legacy JSON approach
        cursor = session
        app_path = f"$.devices.{device_id}.apps.{json.dumps(iname)}"
        path = f"{app_path}.{field}"

        cursor.execute(
            """
            UPDATE json_data
            SET data = json_set(data, ?, ?)
            WHERE username = ? AND json_type(data, ?) IS NOT NULL
            """,
            (path, value, username, app_path),
        )
        logger.debug(f"Queued update for {field} for app {iname} on device {device_id}")


def get_device_webp_dir(device_id: str, create: bool = True) -> Path:
    """Get the WebP directory for a device."""
    path = get_data_dir() / "webp" / secure_filename(device_id)
    if not path.exists() and create:
        path.mkdir(parents=True, exist_ok=True)
    return path


def get_device_by_id(session: Session, device_id: str) -> Device | None:
    """Get a device by ID."""
    device_db = db_ops.get_device_by_id(session, device_id)
    if device_db:
        return db_ops.load_device_full(session, device_db)
    return None


def _remove_corrupt_apps(
    user_data: dict[str, Any], error: ValidationError
) -> tuple[dict[str, Any], list[str]]:
    """Remove corrupt app entries from user data based on validation errors.

    Returns:
        Tuple of (cleaned_user_data, list_of_removed_app_ids)
    """
    removed_apps = []

    # Parse validation errors to find corrupt apps
    for err in error.errors():
        loc = err.get("loc", ())
        # Look for errors in the path: devices.<device_id>.apps.<app_id>.<field>
        if len(loc) >= 4 and loc[0] == "devices" and loc[2] == "apps":
            device_id = loc[1]
            app_id = loc[3]

            # Remove the corrupt app
            devices = user_data.get("devices")
            if isinstance(devices, dict):
                device = devices.get(device_id)
                if isinstance(device, dict):
                    apps = device.get("apps")
                    if isinstance(apps, dict) and app_id in apps:
                        del apps[app_id]
                        removed_apps.append(f"{device_id}/app:{app_id}")
                        logger.warning(
                            f"Removed corrupt app entry {app_id} from device {device_id} "
                            f"(missing field: {loc[-1]})"
                        )

    return user_data, removed_apps


def get_user_by_device_id(session: Session, device_id: str) -> User | None:
    """Get a user by device ID."""
    user_db = db_ops.get_user_by_device_id(session, device_id)
    if user_db:
        try:
            user = db_ops.load_user_full(session, user_db)
            logger.debug(
                f"Loaded user {user_db.username} with {len(user.devices)} devices"
            )
            device = user.devices.get(device_id)
            if device:
                logger.debug(f"Device {device_id} has {len(device.apps)} apps")
            else:
                logger.error(f"Device {device_id} not found in user.devices")
            return user
        except Exception as e:
            logger.error(
                f"Error loading user for device {device_id}: {e}", exc_info=True
            )
            return None
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


def check_firmware_bins_available() -> bool:
    """Check if any firmware bin files are available in the data directory.

    Returns:
        True if at least one firmware bin file exists, False otherwise.
    """
    firmware_dir = get_data_dir() / "firmware"
    if not firmware_dir.exists():
        return False

    # Check for any of the expected firmware files
    # NOTE: This list must be kept in sync with `firmware_name_mapping`
    # in `tronbyt_server/firmware_utils.py`.
    firmware_files = [
        "tidbyt-gen1.bin",
        "tidbyt-gen1_swap.bin",
        "tidbyt-gen2.bin",
        "pixoticker.bin",
        "tronbyt-S3.bin",
        "tronbyt-s3-wide.bin",
        "matrixportal-s3.bin",
        "matrixportal-s3-waveshare.bin",
    ]

    return any((firmware_dir / filename).exists() for filename in firmware_files)


def get_user_by_api_key(session: Session, api_key: str) -> User | None:
    """Get a user by API key."""
    user_db = db_ops.get_user_by_api_key(session, api_key)
    if user_db:
        return db_ops.load_user_full(session, user_db)
    return None


def get_pushed_app(user: User, device_id: str, installation_id: str) -> App | None:
    """Get a pushed app."""
    device = user.devices.get(device_id)
    if not device:
        return None

    apps = device.apps
    if installation_id in apps:
        return apps[installation_id]

    return App(
        id=installation_id,
        path=f"pushed/{installation_id}",
        iname=installation_id,
        name="pushed",
        uinterval=10,
        display_time=0,
        notes="",
        enabled=True,
        pushed=True,
        order=len(apps),
    )


def add_pushed_app(session: Session, device_id: str, installation_id: str) -> None:
    """Add a pushed app to a device."""
    user = get_user_by_device_id(session, device_id)
    if not user:
        raise ValueError("User not found")

    device = user.devices.get(device_id)
    if not device:
        raise ValueError("Device not found")

    app = get_pushed_app(user, device_id, installation_id)
    if not app:
        return

    save_app(session, device_id, app)


def save_app(session: Session, device_id: str, app: App) -> bool:
    """Save an app to the database."""
    if not app.iname:
        return True  # Nothing to save if iname is missing

    try:
        db_ops.save_app_full(session, device_id, app)
        logger.debug(f"Saved app {app.iname} for device {device_id}")
        return True
    except Exception as e:
        logger.error(f"Could not save app {app.iname} for device {device_id}: {e}")
        return False


def save_render_messages(
    session: Session, user: User, device: Device, app: App, messages: list[str]
) -> None:
    """Save render messages from pixlet."""
    app.render_messages = messages
    try:
        # Get the app from database and update render_messages
        app_db = db_ops.get_app_by_device_and_iname(session, device.id, app.iname)
        if app_db:
            app_db.render_messages = messages
            session.add(app_db)
            session.commit()
            logger.debug(
                "Saved render_messages for app %s on device %s for user %s",
                app.iname,
                device.id,
                user.username,
            )
        else:
            logger.warning(
                f"App {app.iname} not found in database, cannot save render messages"
            )
    except Exception as e:
        logger.error(
            f"Could not save render messages for app {app.iname} for user {user.username}: {e}"
        )


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
