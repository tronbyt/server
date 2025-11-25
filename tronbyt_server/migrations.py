"""Database migration utilities for Tronbyt Server.

This module handles automatic migration from the old JSON-based storage
to the new SQLModel relational database format.
"""

import json
import logging
import sqlite3
from datetime import datetime
from pathlib import Path
from typing import Any

from sqlmodel import Session, select

from tronbyt_server.db_models import (
    AppDB,
    DeviceDB,
    LocationDB,
    RecurrencePatternDB,
    SystemSettingsDB,
    UserDB,
    create_db_and_tables,
    engine,
)
from tronbyt_server.models import App, Device, User

logger = logging.getLogger(__name__)


class MigrationStats:
    """Track migration statistics."""

    def __init__(self) -> None:
        self.users_migrated = 0
        self.devices_migrated = 0
        self.apps_migrated = 0
        self.locations_migrated = 0
        self.recurrence_patterns_migrated = 0
        self.errors: list[str] = []


def migrate_user(user: User, session: Session, stats: MigrationStats) -> UserDB | None:
    """Migrate a single user and all their devices/apps to SQLModel tables."""
    try:
        # Create user record
        user_db = UserDB(
            username=user.username,
            password=user.password,
            email=user.email,
            api_key=user.api_key,
            theme_preference=user.theme_preference.value,
            app_repo_url=user.app_repo_url,
        )
        session.add(user_db)
        session.flush()
        stats.users_migrated += 1

        # Migrate all devices for this user
        for device_id, device in user.devices.items():
            migrate_device(device, user_db.id, session, stats)

        return user_db

    except Exception as e:
        error_msg = f"Failed to migrate user {user.username}: {e}"
        stats.errors.append(error_msg)
        logger.error(error_msg)
        return None


def migrate_device(
    device: Device, user_id: int, session: Session, stats: MigrationStats
) -> DeviceDB | None:
    """Migrate a single device and all its apps/location."""
    try:
        # Helper to convert time to string
        def time_to_str(t: Any) -> str | None:
            if t is None:
                return None
            if hasattr(t, "hour") and hasattr(t, "minute"):
                return f"{t.hour:02d}:{t.minute:02d}"
            return str(t) if t else None

        # Convert device.info to dict if it's a Pydantic model
        device_info = device.info
        if hasattr(device_info, "model_dump"):
            device_info = device_info.model_dump()
        elif hasattr(device_info, "dict"):
            device_info = device_info.dict()
        elif not isinstance(device_info, dict):
            device_info = {}

        # Create device record
        device_db = DeviceDB(
            id=device.id,
            name=device.name,
            type=device.type.value,
            api_key=device.api_key,
            img_url=device.img_url,
            ws_url=device.ws_url,
            notes=device.notes,
            brightness=device.brightness.as_percent,
            custom_brightness_scale=device.custom_brightness_scale,
            night_brightness=device.night_brightness.as_percent,
            dim_brightness=(
                device.dim_brightness.as_percent
                if device.dim_brightness is not None
                else None
            ),
            night_mode_enabled=device.night_mode_enabled,
            night_mode_app=device.night_mode_app,
            night_start=time_to_str(device.night_start),
            night_end=time_to_str(device.night_end),
            dim_time=time_to_str(device.dim_time),
            default_interval=device.default_interval,
            timezone=device.timezone,
            last_app_index=device.last_app_index,
            pinned_app=device.pinned_app,
            interstitial_enabled=device.interstitial_enabled,
            interstitial_app=device.interstitial_app,
            last_seen=device.last_seen,
            info=device_info,
            user_id=user_id,
        )
        session.add(device_db)
        session.flush()
        stats.devices_migrated += 1

        # Migrate location if exists
        if device.location:
            location_db = LocationDB(
                locality=device.location.locality,
                description=device.location.description,
                place_id=device.location.place_id,
                timezone=device.location.timezone,
                lat=device.location.lat,
                lng=device.location.lng,
                device_id=device.id,
            )
            session.add(location_db)
            stats.locations_migrated += 1

        # Migrate all apps
        for app_iname, app in device.apps.items():
            migrate_app(app, device.id, session, stats)

        return device_db

    except Exception as e:
        error_msg = f"Failed to migrate device {device.id}: {e}"
        stats.errors.append(error_msg)
        logger.error(error_msg)
        return None


def migrate_app(app: App, device_id: str, session: Session, stats: MigrationStats) -> AppDB | None:
    """Migrate a single app and its recurrence pattern."""
    try:
        # Helper to convert time to string
        def time_to_str(t: Any) -> str | None:
            if t is None:
                return None
            if hasattr(t, "hour") and hasattr(t, "minute"):
                return f"{t.hour:02d}:{t.minute:02d}"
            return str(t) if t else None

        # Create app record
        app_db = AppDB(
            iname=app.iname,
            name=app.name,
            uinterval=app.uinterval,
            display_time=app.display_time,
            notes=app.notes,
            enabled=app.enabled,
            pushed=app.pushed,
            order=app.order,
            last_render=app.last_render,
            last_render_duration=int(app.last_render_duration.total_seconds()),
            path=app.path,
            start_time=time_to_str(app.start_time),
            end_time=time_to_str(app.end_time),
            days=[day.value for day in app.days],
            use_custom_recurrence=app.use_custom_recurrence,
            recurrence_type=app.recurrence_type.value,
            recurrence_interval=app.recurrence_interval,
            recurrence_start_date=app.recurrence_start_date,
            recurrence_end_date=app.recurrence_end_date,
            config=app.config,
            empty_last_render=app.empty_last_render,
            render_messages=app.render_messages,
            autopin=app.autopin,
            device_id=device_id,
        )
        session.add(app_db)
        session.flush()
        stats.apps_migrated += 1

        # Migrate recurrence pattern if exists
        if app.recurrence_pattern:
            pattern_db = RecurrencePatternDB(
                day_of_month=app.recurrence_pattern.day_of_month,
                day_of_week=app.recurrence_pattern.day_of_week,
                weekdays=[wd.value for wd in app.recurrence_pattern.weekdays]
                if app.recurrence_pattern.weekdays
                else [],
                app_id=app_db.id,
            )
            session.add(pattern_db)
            stats.recurrence_patterns_migrated += 1

        return app_db

    except Exception as e:
        error_msg = f"Failed to migrate app {app.iname}: {e}"
        stats.errors.append(error_msg)
        logger.error(error_msg)
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
                    user = User.model_validate(user_data)
                    migrate_user(user, session, stats)
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
            # Rename old table
            logger.info("Renaming json_data table to json_data_backup...")
            conn = sqlite3.connect(db_path)
            cursor = conn.cursor()
            cursor.execute("ALTER TABLE json_data RENAME TO json_data_backup")
            conn.commit()
            conn.close()
            logger.info("Old table backed up successfully")

            # Log summary
            logger.info("=" * 70)
            logger.info("MIGRATION SUMMARY")
            logger.info(f"Users migrated:          {stats.users_migrated}")
            logger.info(f"Devices migrated:        {stats.devices_migrated}")
            logger.info(f"Apps migrated:           {stats.apps_migrated}")
            logger.info(f"Locations migrated:      {stats.locations_migrated}")
            logger.info(f"Recurrence patterns:     {stats.recurrence_patterns_migrated}")
            logger.info("=" * 70)

            return True
        else:
            logger.error("Validation failed - keeping original json_data table")
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
                logger.warning(f"User count mismatch: {users_count} vs {stats.users_migrated}")
                return False

            if devices_count != stats.devices_migrated:
                logger.warning(f"Device count mismatch: {devices_count} vs {stats.devices_migrated}")
                return False

            if apps_count != stats.apps_migrated:
                logger.warning(f"App count mismatch: {apps_count} vs {stats.apps_migrated}")
                return False

            logger.info(f"✓ Validation passed: {users_count} users, {devices_count} devices, {apps_count} apps")
            return True

    except Exception as e:
        logger.error(f"Validation failed with error: {e}")
        return False
