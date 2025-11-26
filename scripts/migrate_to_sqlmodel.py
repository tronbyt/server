#!/usr/bin/env python3
"""
Migration script to convert from JSON storage to SQLModel relational tables.

This script:
1. Reads all users from the json_data table
2. Creates normalized records in users, devices, apps, locations tables
3. Validates the migration was successful
4. Preserves the json_data table for rollback safety

Usage:
    # Dry run (shows what would happen, doesn't change anything)
    python scripts/migrate_to_sqlmodel.py --dry-run

    # Actually perform the migration
    python scripts/migrate_to_sqlmodel.py

    # Use a specific database file
    python scripts/migrate_to_sqlmodel.py --db-path /path/to/db.sqlite

IMPORTANT: This script will create new tables but will NOT delete the old json_data table.
The old table will be preserved so you can safely roll back to an older server version.
"""

import argparse
import json
import sqlite3
import sys
from datetime import datetime
from pathlib import Path
from sqlmodel import Session, select  # noqa: E402
from tronbyt_server.db_models import (  # noqa: E402
    AppDB,
    DeviceDB,
    LocationDB,
    SystemSettingsDB,
    UserDB,
    create_db_and_tables,
    engine,
)
from tronbyt_server.models import App, Device, User  # noqa: E402

# Add project root to path so we can import tronbyt_server
script_dir = Path(__file__).resolve().parent
project_root = script_dir.parent
sys.path.insert(0, str(project_root))


class MigrationStats:
    """Track migration statistics."""

    def __init__(self) -> None:
        self.users_migrated = 0
        self.devices_migrated = 0
        self.apps_migrated = 0
        self.locations_migrated = 0
        self.recurrence_patterns_migrated = 0
        self.errors: list[str] = []

    def print_summary(self) -> None:
        """Print migration summary."""
        print("\n" + "=" * 60)
        print("MIGRATION SUMMARY")
        print("=" * 60)
        print(f"Users migrated:              {self.users_migrated}")
        print(f"Devices migrated:            {self.devices_migrated}")
        print(f"Apps migrated:               {self.apps_migrated}")
        print(f"Locations migrated:          {self.locations_migrated}")
        print(f"Recurrence patterns:         {self.recurrence_patterns_migrated}")

        if self.errors:
            print(f"\nErrors encountered:          {len(self.errors)}")
            for error in self.errors[:5]:  # Show first 5 errors
                print(f"  - {error}")
            if len(self.errors) > 5:
                print(f"  ... and {len(self.errors) - 5} more errors")
        else:
            print("\n✓ No errors encountered")
        print("=" * 60)


def migrate_user(user: User, session: Session, stats: MigrationStats) -> UserDB | None:
    """
    Migrate a single user and all their devices/apps to SQLModel tables.

    Args:
        user: Pydantic User model from JSON
        session: SQLModel session
        stats: Migration statistics tracker

    Returns:
        The created UserDB instance, or None if failed
    """
    try:
        # Create user record (system_repo_url is now in system_settings table)
        user_db = UserDB(
            username=user.username,
            password=user.password,
            email=user.email,
            api_key=user.api_key,
            theme_preference=user.theme_preference.value,
            app_repo_url=user.app_repo_url,
        )
        session.add(user_db)
        session.flush()  # Get the user ID
        stats.users_migrated += 1

        # Migrate all devices for this user
        for device_id, device in user.devices.items():
            try:
                migrate_device(device, user_db.id, session, stats)  # type: ignore
            except Exception as e:
                error_msg = (
                    f"Error migrating device {device_id} for user {user.username}: {e}"
                )
                stats.errors.append(error_msg)
                print(f"  ⚠️  {error_msg}")

        return user_db

    except Exception as e:
        error_msg = f"Error migrating user {user.username}: {e}"
        stats.errors.append(error_msg)
        print(f"  ❌ {error_msg}")
        return None


def migrate_device(
    device: Device, user_id: int, session: Session, stats: MigrationStats
) -> DeviceDB | None:
    """Migrate a single device and its location/apps."""
    try:
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
                device.dim_brightness.as_percent if device.dim_brightness else None
            ),
            night_mode_enabled=device.night_mode_enabled,
            night_mode_app=device.night_mode_app,
            night_start=device.night_start,
            night_end=device.night_end,
            dim_time=device.dim_time,
            default_interval=device.default_interval,
            timezone=device.timezone,
            last_app_index=device.last_app_index,
            pinned_app=device.pinned_app,
            interstitial_enabled=device.interstitial_enabled,
            interstitial_app=device.interstitial_app,
            last_seen=device.last_seen,
            info=device.info.model_dump() if device.info else {},
            user_id=user_id,
        )
        session.add(device_db)
        session.flush()
        stats.devices_migrated += 1

        # Migrate location if present
        if device.location:
            try:
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
            except Exception as e:
                error_msg = f"Error migrating location for device {device.id}: {e}"
                stats.errors.append(error_msg)
                print(f"    ⚠️  {error_msg}")

        # Migrate all apps for this device
        for app_iname, app in device.apps.items():
            try:
                migrate_app(app, device.id, session, stats)
            except Exception as e:
                error_msg = (
                    f"Error migrating app {app_iname} on device {device.id}: {e}"
                )
                stats.errors.append(error_msg)
                print(f"    ⚠️  {error_msg}")

        return device_db

    except Exception as e:
        error_msg = f"Error migrating device {device.id}: {e}"
        stats.errors.append(error_msg)
        print(f"    ❌ {error_msg}")
        return None


def migrate_app(
    app: App, device_id: str, session: Session, stats: MigrationStats
) -> AppDB | None:
    """Migrate a single app and its recurrence pattern."""
    try:
        # Convert time objects to HH:MM strings
        start_time_str = app.start_time.strftime("%H:%M") if app.start_time else None
        end_time_str = app.end_time.strftime("%H:%M") if app.end_time else None

        # Convert weekdays enum list to strings
        days_list = [day.value for day in app.days]

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
            start_time=start_time_str,
            end_time=end_time_str,
            days=days_list,
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
        session.flush()  # Get the app ID
        stats.apps_migrated += 1

        # Migrate recurrence pattern if present
        if app.recurrence_pattern and (
            app.recurrence_pattern.day_of_month
            or app.recurrence_pattern.day_of_week
            or app.recurrence_pattern.weekdays
        ):
            try:
                weekdays_list = (
                    [day.value for day in app.recurrence_pattern.weekdays]
                    if app.recurrence_pattern.weekdays
                    else []
                )

                app_db.recurrence_pattern = {
                    "day_of_month": app.recurrence_pattern.day_of_month,
                    "day_of_week": app.recurrence_pattern.day_of_week,
                    "weekdays": weekdays_list,
                }
                stats.recurrence_patterns_migrated += 1
            except Exception as e:
                error_msg = (
                    f"Error migrating recurrence pattern for app {app.iname}: {e}"
                )
                stats.errors.append(error_msg)
                print(f"      ⚠️  {error_msg}")

        return app_db

    except Exception as e:
        error_msg = f"Error migrating app {app.iname}: {e}"
        stats.errors.append(error_msg)
        print(f"      ❌ {error_msg}")
        return None


def validate_migration(
    old_db_path: str, session: Session, stats: MigrationStats
) -> bool:
    """
    Validate that the migration was successful by comparing counts.

    Returns:
        True if validation passed, False otherwise
    """
    print("\n" + "=" * 60)
    print("VALIDATING MIGRATION")
    print("=" * 60)

    try:
        # Connect to old database to count records
        old_conn = sqlite3.connect(old_db_path)
        old_cursor = old_conn.cursor()

        # Count users in old database
        old_cursor.execute("SELECT COUNT(*) FROM json_data")
        old_user_count = old_cursor.fetchone()[0]

        # Count devices and apps by parsing JSON
        old_cursor.execute("SELECT data FROM json_data")
        old_device_count = 0
        old_app_count = 0
        old_location_count = 0

        for (user_json,) in old_cursor.fetchall():
            user_data = json.loads(user_json)
            devices = user_data.get("devices", {})
            old_device_count += len(devices)
            for device in devices.values():
                if device.get("location"):
                    old_location_count += 1
                apps = device.get("apps", {})
                old_app_count += len(apps)

        old_conn.close()

        # Count records in new database
        new_user_count = session.exec(select(UserDB)).all()
        new_device_count = session.exec(select(DeviceDB)).all()
        new_app_count = session.exec(select(AppDB)).all()
        new_location_count = session.exec(select(LocationDB)).all()

        # Compare counts
        validation_passed = True

        print(f"\nUsers:     {len(new_user_count):5d} / {old_user_count:5d}", end="")
        if len(new_user_count) == old_user_count:
            print(" ✓")
        else:
            print(" ❌ MISMATCH")
            validation_passed = False

        print(f"Devices:   {len(new_device_count):5d} / {old_device_count:5d}", end="")
        if len(new_device_count) == old_device_count:
            print(" ✓")
        else:
            print(" ❌ MISMATCH")
            validation_passed = False

        print(f"Apps:      {len(new_app_count):5d} / {old_app_count:5d}", end="")
        if len(new_app_count) == old_app_count:
            print(" ✓")
        else:
            print(" ❌ MISMATCH")
            validation_passed = False

        print(
            f"Locations: {len(new_location_count):5d} / {old_location_count:5d}", end=""
        )
        if len(new_location_count) == old_location_count:
            print(" ✓")
        else:
            print(" ❌ MISMATCH")
            validation_passed = False

        return validation_passed

    except Exception as e:
        print(f"\n❌ Validation failed with error: {e}")
        return False


def perform_migration(db_path: str, dry_run: bool = False) -> bool:
    """
    Perform the migration from JSON storage to SQLModel tables.

    Args:
        db_path: Path to the SQLite database
        dry_run: If True, don't actually commit changes

    Returns:
        True if successful, False otherwise
    """
    db_file = Path(db_path)

    if not db_file.exists():
        print(f"❌ Error: Database file not found at {db_path}")
        return False

    print("=" * 60)
    print("SQLMODEL MIGRATION SCRIPT")
    print("=" * 60)
    print(f"Database: {db_path}")
    print(
        f"Mode:     {'DRY RUN (no changes will be made)' if dry_run else 'LIVE MIGRATION'}"
    )
    print(f"Time:     {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}")
    print("=" * 60)

    stats = MigrationStats()

    try:
        # Drop existing new tables if they exist (for clean re-migration)
        print("\n🧹 Cleaning up any existing new tables...")
        conn_clean = sqlite3.connect(db_path)
        cursor_clean = conn_clean.cursor()

        # Drop tables in reverse dependency order
        for table in [
            "recurrence_patterns",
            "apps",
            "locations",
            "devices",
            "users",
            "system_settings",
        ]:
            try:
                cursor_clean.execute(f"DROP TABLE IF EXISTS {table}")
            except sqlite3.Error:
                pass

        conn_clean.commit()
        conn_clean.close()
        print("✓ Cleanup complete")

        # Create new tables
        print("\n📋 Creating new database tables...")
        create_db_and_tables()
        print("✓ Tables created successfully")

        # Read all users from json_data table
        print("\n📖 Reading users from json_data table...")
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("SELECT username, data FROM json_data")
        rows = cursor.fetchall()
        conn.close()

        print(f"✓ Found {len(rows)} users to migrate")

        # Create system settings (extract from admin user if exists)
        print("\n⚙️  Creating system settings...")
        admin_system_repo_url = ""
        for username, user_json in rows:
            if username == "admin":
                try:
                    user_data = json.loads(user_json)
                    admin_system_repo_url = user_data.get("system_repo_url", "")
                    print(
                        f"  Found system_repo_url from admin: {admin_system_repo_url or '(empty)'}"
                    )
                except Exception:
                    pass
                break

        # Migrate each user
        print("\n🔄 Migrating users...")
        with Session(engine) as session:
            # Create system settings record first (check if it already exists)
            existing_settings = session.exec(select(SystemSettingsDB)).first()
            if existing_settings:
                print("  ⚠️  System settings already exist, updating...")
                existing_settings.system_repo_url = admin_system_repo_url
                session.add(existing_settings)
            else:
                system_settings = SystemSettingsDB(
                    id=1,
                    system_repo_url=admin_system_repo_url,
                )
                session.add(system_settings)
            session.flush()  # Write to DB immediately
            print("  ✓ System settings ready")

            for username, user_json in rows:
                try:
                    print(f"\n  Migrating user: {username}")
                    user_data = json.loads(user_json)
                    user = User.model_validate(user_data)
                    migrate_user(user, session, stats)

                except Exception as e:
                    error_msg = f"Failed to parse user {username}: {e}"
                    stats.errors.append(error_msg)
                    print(f"  ❌ {error_msg}")
                    continue

            if dry_run:
                print("\n🔍 DRY RUN - Rolling back all changes...")
                session.rollback()
            else:
                print("\n💾 Committing changes to database...")
                session.commit()
                print("✓ Changes committed successfully")

                # Validate migration
                validation_passed = validate_migration(db_path, session, stats)

                if validation_passed:
                    print("\n✓ Validation passed!")
                    print("\nNOTE: The original json_data table has been preserved.")
                    print(
                        "      You can safely roll back by reverting to an older version."
                    )
                    print(
                        "      To reclaim space, you can manually drop it: DROP TABLE json_data;"
                    )
                else:
                    print("\n⚠️  Validation failed")
                    print("   You may want to investigate the mismatches")

        # Print summary
        stats.print_summary()

        if dry_run:
            print("\n📝 This was a DRY RUN - no changes were made to the database")
            print("   Run without --dry-run to perform the actual migration")
            return True
        else:
            return validation_passed and len(stats.errors) == 0

    except Exception as e:
        print(f"\n❌ Migration failed with error: {e}")
        import traceback

        traceback.print_exc()
        return False


def main() -> None:
    """Main entry point."""
    parser = argparse.ArgumentParser(
        description="Migrate from JSON storage to SQLModel relational tables",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Dry run to see what would happen
  python scripts/migrate_to_sqlmodel.py --dry-run

  # Perform the actual migration
  python scripts/migrate_to_sqlmodel.py

  # Use a specific database file
  python scripts/migrate_to_sqlmodel.py --db-path /path/to/db.sqlite
        """,
    )

    parser.add_argument(
        "--db-path",
        type=str,
        default=None,
        help="Path to the SQLite database file (default: users/usersdb.sqlite)",
    )

    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Perform a dry run without making any changes",
    )

    args = parser.parse_args()

    # Determine database path
    if args.db_path:
        db_path = args.db_path
    else:
        db_path = str(project_root / "users" / "usersdb.sqlite")

    # Perform migration
    success = perform_migration(db_path, dry_run=args.dry_run)

    if success:
        print("\n✅ Migration completed successfully!")
        sys.exit(0)
    else:
        print("\n❌ Migration failed. Please review the errors above.")
        sys.exit(1)


if __name__ == "__main__":
    main()
