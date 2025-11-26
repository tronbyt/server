"""Database migration utilities for Tronbyt Server.

This module handles automatic migration from the old JSON-based storage
to the new SQLModel relational database format.
"""

import json
import logging
import sqlite3
from datetime import datetime
from typing import Any

from sqlmodel import Session, select

from tronbyt_server.db_models import (
    AppDB,
    DeviceDB,
    LocationDB,
    SystemSettingsDB,
    UserDB,
    create_db_and_tables,
    engine,
)

logger = logging.getLogger(__name__)


class MigrationStats:
    """Track migration statistics."""

    def __init__(self) -> None:
        self.users_migrated = 0
        self.devices_migrated = 0
        self.apps_migrated = 0
        self.locations_migrated = 0
        self.recurrence_patterns_migrated = 0
        self.devices_skipped = 0
        self.apps_skipped = 0
        self.skipped: list[str] = []  # Records that were skipped
        self.errors: list[str] = []  # Fatal errors only


def migrate_user_from_dict(
    user_data: dict[str, Any], session: Session, stats: MigrationStats
) -> UserDB | None:
    """Migrate a single user and all their devices/apps from raw dict data.

    This function handles validation at a granular level, allowing invalid apps
    or devices to be skipped while still migrating valid data.
    """
    username = user_data.get("username", "unknown")

    try:
        # Validate and create user record from user-level fields only
        user_db = UserDB(
            username=username,
            password=user_data.get("password") or "",
            email=user_data.get("email"),
            api_key=user_data.get("api_key") or "",
            theme_preference=user_data.get("theme_preference") or "system",
            app_repo_url=user_data.get("app_repo_url"),
        )
        session.add(user_db)
        session.flush()
        stats.users_migrated += 1

        # Ensure user_db has an ID before migrating devices
        if user_db.id is None:
            raise ValueError(f"User {username} was not assigned an ID")

        # Migrate all devices for this user
        devices = user_data.get("devices", {})
        for device_id, device_data in devices.items():
            migrate_device_from_dict(device_data, user_db.id, session, stats)

        return user_db

    except Exception as e:
        error_msg = f"Failed to migrate user {username}: {e}"
        stats.errors.append(error_msg)
        logger.error(error_msg)
        return None


def migrate_device_from_dict(
    device_data: dict[str, Any], user_id: int, session: Session, stats: MigrationStats
) -> DeviceDB | None:
    """Migrate a single device and all its apps/location from raw dict data.

    This function validates each app individually, allowing invalid apps to be
    skipped while still migrating the device and valid apps.
    """
    device_id = device_data.get("id", "unknown")

    try:
        # Helper to convert time to string
        def time_to_str(t: Any) -> str | None:
            if t is None:
                return None
            if hasattr(t, "hour") and hasattr(t, "minute"):
                return f"{t.hour:02d}:{t.minute:02d}"
            return str(t) if t else None

        # Helper to parse datetime strings
        def parse_datetime(dt: Any) -> datetime | None:
            if dt is None:
                return None
            if isinstance(dt, datetime):
                return dt
            if isinstance(dt, str):
                try:
                    # Try ISO format first (most common)
                    return datetime.fromisoformat(dt.replace("Z", "+00:00"))
                except Exception:
                    return None
            return None

        # Extract brightness values - handle both dict and numeric formats
        brightness = device_data.get("brightness")
        if isinstance(brightness, dict):
            brightness = brightness.get("as_percent", 100)
        elif brightness is None:
            brightness = 100

        night_brightness = device_data.get("night_brightness")
        if isinstance(night_brightness, dict):
            night_brightness = night_brightness.get("as_percent", 10)
        elif night_brightness is None:
            night_brightness = 10

        dim_brightness = device_data.get("dim_brightness")
        if isinstance(dim_brightness, dict):
            dim_brightness = dim_brightness.get("as_percent")
        elif dim_brightness is not None:
            pass  # Use as-is if it's already a number

        # Get device info
        device_info = device_data.get("info", {})
        if not isinstance(device_info, dict):
            device_info = {}

        # Map old device type to new enum values
        device_type = device_data.get("type") or "tidbyt"
        if device_type == "tidbyt":
            device_type = "tidbyt_gen1"  # Default to gen1 for old "tidbyt" entries

        # Create device record
        device_db = DeviceDB(
            id=device_id,
            name=device_data.get("name") or "",
            type=device_type,
            api_key=device_data.get("api_key") or "",
            img_url=device_data.get("img_url"),
            ws_url=device_data.get("ws_url"),
            notes=device_data.get("notes") or "",
            brightness=brightness,
            custom_brightness_scale=device_data.get("custom_brightness_scale"),
            night_brightness=night_brightness,
            dim_brightness=dim_brightness,
            night_mode_enabled=device_data.get("night_mode_enabled", False),
            night_mode_app=device_data.get("night_mode_app"),
            night_start=time_to_str(device_data.get("night_start")),
            night_end=time_to_str(device_data.get("night_end")),
            dim_time=time_to_str(device_data.get("dim_time")),
            default_interval=device_data.get("default_interval") or 15,
            timezone=device_data.get("timezone") or "UTC",
            last_app_index=device_data.get("last_app_index") or 0,
            pinned_app=device_data.get("pinned_app"),
            interstitial_enabled=device_data.get("interstitial_enabled") or False,
            interstitial_app=device_data.get("interstitial_app"),
            last_seen=parse_datetime(device_data.get("last_seen")),
            info=device_info,
            user_id=user_id,
        )
        session.add(device_db)
        session.flush()
        stats.devices_migrated += 1

        # Migrate location if exists
        location_data = device_data.get("location")
        if location_data:
            try:
                location_db = LocationDB(
                    locality=location_data.get("locality"),
                    description=location_data.get("description"),
                    place_id=location_data.get("place_id"),
                    timezone=location_data.get("timezone"),
                    lat=location_data.get("lat"),
                    lng=location_data.get("lng"),
                    device_id=device_id,
                )
                session.add(location_db)
                stats.locations_migrated += 1
            except Exception as e:
                skip_msg = f"Skipping location for device {device_id}: {e}"
                stats.skipped.append(skip_msg)
                logger.warning(skip_msg)

        # Migrate all apps - validate each individually
        apps = device_data.get("apps", {})
        for app_iname, app_data in apps.items():
            migrate_app_from_dict(app_data, device_id, session, stats)

        return device_db

    except Exception as e:
        skip_msg = f"Skipping incomplete device {device_id}: {e}"
        stats.skipped.append(skip_msg)
        stats.devices_skipped += 1
        logger.warning(skip_msg)
        return None


def migrate_app_from_dict(
    app_data: dict[str, Any], device_id: str, session: Session, stats: MigrationStats
) -> AppDB | None:
    """Migrate a single app and its recurrence pattern from raw dict data."""
    app_iname = app_data.get("iname", "unknown")

    # Skip apps with invalid/missing inames
    if not app_iname or app_iname == "unknown":
        skip_msg = f"Skipping app with invalid iname: {app_iname}"
        stats.skipped.append(skip_msg)
        stats.apps_skipped += 1
        logger.warning(skip_msg)
        return None

    try:
        # Helper to convert time to string
        def time_to_str(t: Any) -> str | None:
            if t is None:
                return None
            if hasattr(t, "hour") and hasattr(t, "minute"):
                return f"{t.hour:02d}:{t.minute:02d}"
            return str(t) if t else None

        # Helper to parse datetime strings
        def parse_datetime(dt: Any) -> datetime | None:
            if dt is None:
                return None
            if isinstance(dt, datetime):
                return dt
            if isinstance(dt, str):
                try:
                    # Try ISO format first (most common)
                    return datetime.fromisoformat(dt.replace("Z", "+00:00"))
                except Exception:
                    return None
            return None

        # Helper to parse date strings (for recurrence dates)
        def parse_date(d: Any) -> Any:
            if d is None:
                return None
            if isinstance(d, str):
                try:
                    # Try ISO format first
                    parsed = datetime.fromisoformat(d.replace("Z", "+00:00"))
                    return parsed.date()
                except Exception:
                    return None
            return d

        # Extract last_render_duration - handle timedelta dict format
        last_render_duration = app_data.get("last_render_duration", 0)
        if isinstance(last_render_duration, dict):
            # timedelta stored as dict with days, seconds, microseconds
            days = last_render_duration.get("days", 0)
            seconds = last_render_duration.get("seconds", 0)
            last_render_duration = days * 86400 + seconds
        elif last_render_duration is None:
            last_render_duration = 0

        # Extract days list
        days = app_data.get("days") or []
        if isinstance(days, list):
            # Days might be strings or objects with 'value' attribute
            days_list = []
            for day in days:
                if isinstance(day, str):
                    days_list.append(day)
                elif isinstance(day, dict) and "value" in day:
                    days_list.append(day["value"])
                else:
                    days_list.append(str(day))
            days = days_list
        else:
            days = []  # Ensure it's always a list

        # Map old recurrence_type values to new enum
        recurrence_type = app_data.get("recurrence_type") or "daily"
        if recurrence_type == "never":
            # Old "never" value doesn't exist in new enum, map to daily
            recurrence_type = "daily"

        # Create app record
        app_db = AppDB(
            iname=app_iname,
            name=app_data.get("name") or "",
            uinterval=app_data.get("uinterval") or 0,
            display_time=app_data.get("display_time") or 15,
            notes=app_data.get("notes"),
            enabled=app_data.get("enabled")
            if app_data.get("enabled") is not None
            else True,
            pushed=app_data.get("pushed") or False,
            order=app_data.get("order") or 0,
            last_render=parse_datetime(app_data.get("last_render")),
            last_render_duration=int(last_render_duration),
            path=app_data.get("path"),
            start_time=time_to_str(app_data.get("start_time")),
            end_time=time_to_str(app_data.get("end_time")),
            days=days,
            use_custom_recurrence=app_data.get("use_custom_recurrence") or False,
            recurrence_type=recurrence_type,
            recurrence_interval=app_data.get("recurrence_interval") or 1,
            recurrence_start_date=parse_date(app_data.get("recurrence_start_date")),
            recurrence_end_date=parse_date(app_data.get("recurrence_end_date")),
            config=app_data.get("config") if app_data.get("config") is not None else {},
            empty_last_render=app_data.get("empty_last_render") or False,
            render_messages=app_data.get("render_messages")
            if app_data.get("render_messages") is not None
            else [],
            autopin=app_data.get("autopin") or False,
            device_id=device_id,
        )
        session.add(app_db)
        session.flush()
        stats.apps_migrated += 1

        # Migrate recurrence pattern as JSON if exists
        recurrence_pattern = app_data.get("recurrence_pattern")
        if recurrence_pattern:
            try:
                weekdays = recurrence_pattern.get("weekdays") or []
                if isinstance(weekdays, list):
                    # Weekdays might be strings or objects with 'value' attribute
                    weekdays_list = []
                    for wd in weekdays:
                        if isinstance(wd, str):
                            weekdays_list.append(wd)
                        elif isinstance(wd, dict) and "value" in wd:
                            weekdays_list.append(wd["value"])
                        else:
                            weekdays_list.append(str(wd))
                    weekdays = weekdays_list
                else:
                    weekdays = []  # Ensure it's always a list

                app_db.recurrence_pattern = {
                    "day_of_month": recurrence_pattern.get("day_of_month"),
                    "day_of_week": recurrence_pattern.get("day_of_week"),
                    "weekdays": weekdays,
                }
                stats.recurrence_patterns_migrated += 1
            except Exception as e:
                skip_msg = f"Skipping recurrence pattern for app {app_iname}: {e}"
                stats.skipped.append(skip_msg)
                logger.warning(skip_msg)

        return app_db

    except Exception as e:
        skip_msg = f"Skipping incomplete app {app_iname}: {e}"
        stats.skipped.append(skip_msg)
        stats.apps_skipped += 1
        logger.warning(skip_msg)
        return None


def perform_json_to_sqlmodel_migration(db_path: str) -> bool:
    """
    Perform automatic migration from JSON storage to SQLModel tables.

    This is called automatically by init_db() when an old database is detected.

    Args:
        db_path: Path to the SQLite database file

    Returns:
        True if migration successful, False otherwise
    """
    logger.info("=" * 70)
    logger.info("AUTOMATIC SQLMODEL MIGRATION")
    logger.info(f"Database: {db_path}")
    logger.info(f"Time: {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    logger.info("=" * 70)

    stats = MigrationStats()

    try:
        # Create new tables
        logger.info("Creating SQLModel tables...")
        create_db_and_tables()

        # Connect to database and read old data
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("SELECT username, data FROM json_data")
        rows = cursor.fetchall()
        conn.close()

        logger.info(f"Found {len(rows)} users to migrate")

        # Migrate data
        with Session(engine) as session:
            # Create system settings
            system_settings = SystemSettingsDB(id=1, system_repo_url="")
            session.add(system_settings)
            session.flush()

            # Migrate each user
            for username, user_json in rows:
                try:
                    logger.info(f"Migrating user: {username}")
                    user_data = json.loads(user_json)
                    migrate_user_from_dict(user_data, session, stats)
                except Exception as e:
                    error_msg = f"Failed to parse user {username}: {e}"
                    stats.errors.append(error_msg)
                    logger.error(error_msg)
                    continue

            # Commit all changes
            logger.info("Committing changes to database...")
            session.commit()

        # Validate migration
        logger.info("Validating migration...")
        validation_passed = validate_migration(db_path, stats)

        if validation_passed and len(stats.errors) == 0:
            # Log summary
            logger.info("=" * 70)
            logger.info("MIGRATION SUMMARY")
            logger.info(f"Users migrated:          {stats.users_migrated}")
            logger.info(f"Devices migrated:        {stats.devices_migrated}")
            logger.info(f"Apps migrated:           {stats.apps_migrated}")
            logger.info(f"Locations migrated:      {stats.locations_migrated}")
            logger.info(
                f"Recurrence patterns:     {stats.recurrence_patterns_migrated}"
            )
            if stats.devices_skipped > 0 or stats.apps_skipped > 0:
                logger.info(f"Devices skipped:         {stats.devices_skipped}")
                logger.info(f"Apps skipped:            {stats.apps_skipped}")
            logger.info("=" * 70)

            if len(stats.skipped) > 0:
                logger.warning(
                    f"⚠️  {len(stats.skipped)} incomplete records were skipped:"
                )
                for skip in stats.skipped:
                    logger.warning(f"  - {skip}")

            logger.info("✅ Migration completed successfully!")
            logger.info("NOTE: The original json_data table has been preserved.")
            logger.info(
                "      You can safely roll back by reverting to an older version."
            )
            logger.info(
                "      To reclaim space, you can manually drop it: DROP TABLE json_data;"
            )

            return True
        else:
            if not validation_passed:
                logger.error("❌ Validation failed - counts don't match")
            if len(stats.errors) > 0:
                logger.error(
                    f"❌ Migration encountered {len(stats.errors)} fatal errors:"
                )
                for error in stats.errors:
                    logger.error(f"  - {error}")
            logger.error("Migration failed - json_data table unchanged")
            return False

    except Exception as e:
        logger.error(f"Migration failed with error: {e}")
        import traceback

        traceback.print_exc()
        return False


def validate_migration(db_path: str, stats: MigrationStats) -> bool:
    """Validate that migration was successful."""
    try:
        with Session(engine) as session:
            # Count migrated records
            users_count = len(session.exec(select(UserDB)).all())
            devices_count = len(session.exec(select(DeviceDB)).all())
            apps_count = len(session.exec(select(AppDB)).all())
            locations_count = len(session.exec(select(LocationDB)).all())

            # Check counts match
            if users_count != stats.users_migrated:
                logger.warning(
                    f"User count mismatch: {users_count} vs {stats.users_migrated}"
                )
                return False

            if devices_count != stats.devices_migrated:
                logger.warning(
                    f"Device count mismatch: {devices_count} vs {stats.devices_migrated}"
                )
                return False

            if apps_count != stats.apps_migrated:
                logger.warning(
                    f"App count mismatch: {apps_count} vs {stats.apps_migrated}"
                )
                return False

            if locations_count != stats.locations_migrated:
                logger.warning(
                    f"Location count mismatch: {locations_count} vs {stats.locations_migrated}"
                )
                return False

            logger.info(
                f"✓ Validation passed: {users_count} users, {devices_count} devices, {apps_count} apps, {locations_count} locations"
            )
            return True

    except Exception as e:
        logger.error(f"Validation failed with error: {e}")
        return False
