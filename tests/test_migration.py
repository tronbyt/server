"""Tests for migration from JSON to SQLModel."""

import json
import sqlite3
import tempfile
from pathlib import Path

from sqlmodel import Session, select, create_engine

from tronbyt_server.db_models import AppDB, DeviceDB, UserDB
from tronbyt_server.migrations import perform_json_to_sqlmodel_migration


def test_migration_skips_invalid_apps_but_keeps_valid_data():
    """Test that migration skips invalid apps while keeping valid parts of user data."""

    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = Path(tmpdir) / "test.sqlite"

        # Create a test database with old json_data table
        conn = sqlite3.connect(str(db_path))
        cursor = conn.cursor()
        cursor.execute(
            """
            CREATE TABLE json_data (
                username TEXT PRIMARY KEY,
                data TEXT
            )
            """
        )

        # Create a user with mixed valid and invalid apps
        user_data = {
            "username": "testuser",
            "password": "hashedpass",
            "email": "test@example.com",
            "api_key": "test_api_key",
            "theme_preference": "dark",
            "app_repo_url": "https://github.com/test/repo",
            "devices": {
                "device1": {
                    "id": "device1",
                    "name": "Test Device",
                    "type": "tidbyt",
                    "api_key": "device_api_key",
                    "brightness": {"as_percent": 100},
                    "night_brightness": {"as_percent": 10},
                    "night_mode_enabled": False,
                    "default_interval": 15,
                    "timezone": "UTC",
                    "last_app_index": 0,
                    "interstitial_enabled": False,
                    "info": {},
                    "apps": {
                        "valid_app_1": {
                            "iname": "valid_app_1",
                            "name": "Valid App 1",
                            "uinterval": 60,
                            "display_time": 15,
                            "enabled": True,
                            "pushed": False,
                            "order": 0,
                            "last_render_duration": {"days": 0, "seconds": 1},
                            "days": ["monday", "tuesday"],
                            "use_custom_recurrence": False,
                            "recurrence_type": "never",
                            "recurrence_interval": 1,
                            "config": {},
                            "empty_last_render": False,
                            "render_messages": [],
                            "autopin": False,
                        },
                        "invalid_app": {
                            # This app is missing critical fields and has invalid data
                            "iname": "invalid_app",
                            "name": None,  # Invalid: should be a string
                            # Missing many required fields
                        },
                        "valid_app_2": {
                            "iname": "valid_app_2",
                            "name": "Valid App 2",
                            "uinterval": 30,
                            "display_time": 10,
                            "enabled": True,
                            "pushed": False,
                            "order": 1,
                            "last_render_duration": {"days": 0, "seconds": 2},
                            "days": [],
                            "use_custom_recurrence": False,
                            "recurrence_type": "never",
                            "recurrence_interval": 1,
                            "config": {},
                            "empty_last_render": False,
                            "render_messages": [],
                            "autopin": False,
                        },
                    },
                }
            },
        }

        cursor.execute(
            "INSERT INTO json_data (username, data) VALUES (?, ?)",
            ("testuser", json.dumps(user_data)),
        )
        conn.commit()
        conn.close()

        # Temporarily override the engine connection to use our test database
        from tronbyt_server import db_models

        original_engine = db_models.engine

        try:
            # Create a new engine for the test database
            db_models.engine = create_engine(f"sqlite:///{db_path}")

            # Run the migration
            result = perform_json_to_sqlmodel_migration(str(db_path))

            # Migration should succeed even with invalid apps
            assert result is True

            # Verify the results
            with Session(db_models.engine) as session:
                # User should be migrated
                users = session.exec(select(UserDB)).all()
                assert len(users) == 1
                user = users[0]
                assert user.username == "testuser"
                assert user.email == "test@example.com"

                # Device should be migrated
                devices = session.exec(select(DeviceDB)).all()
                assert len(devices) == 1
                device = devices[0]
                assert device.id == "device1"
                assert device.name == "Test Device"

                # Valid apps should be migrated, invalid app should be skipped
                apps = session.exec(select(AppDB)).all()
                assert len(apps) == 2  # Only 2 valid apps

                app_inames = {app.iname for app in apps}
                assert "valid_app_1" in app_inames
                assert "valid_app_2" in app_inames
                assert "invalid_app" not in app_inames  # Invalid app was skipped

                # Verify app details
                valid_app_1 = next(app for app in apps if app.iname == "valid_app_1")
                assert valid_app_1.name == "Valid App 1"
                assert valid_app_1.uinterval == 60
                assert valid_app_1.days == ["monday", "tuesday"]

        finally:
            # Restore original engine
            db_models.engine = original_engine


def test_migration_handles_invalid_device_gracefully():
    """Test that migration can skip an invalid device while keeping the user."""

    with tempfile.TemporaryDirectory() as tmpdir:
        db_path = Path(tmpdir) / "test.sqlite"

        # Create a test database with old json_data table
        conn = sqlite3.connect(str(db_path))
        cursor = conn.cursor()
        cursor.execute(
            """
            CREATE TABLE json_data (
                username TEXT PRIMARY KEY,
                data TEXT
            )
            """
        )

        # Create a user with a valid device and an invalid device
        user_data = {
            "username": "testuser",
            "password": "hashedpass",
            "email": "test@example.com",
            "api_key": "test_api_key",
            "theme_preference": "dark",
            "devices": {
                "valid_device": {
                    "id": "valid_device",
                    "name": "Valid Device",
                    "type": "tidbyt",
                    "api_key": "device_api_key",
                    "brightness": {"as_percent": 100},
                    "night_brightness": {"as_percent": 10},
                    "default_interval": 15,
                    "timezone": "UTC",
                    "apps": {},
                },
                "invalid_device": {
                    # Missing critical fields
                    "id": "invalid_device",
                    # Missing most required fields
                    "type": None,  # Invalid type
                },
            },
        }

        cursor.execute(
            "INSERT INTO json_data (username, data) VALUES (?, ?)",
            ("testuser", json.dumps(user_data)),
        )
        conn.commit()
        conn.close()

        from tronbyt_server import db_models

        original_engine = db_models.engine

        try:
            db_models.engine = create_engine(f"sqlite:///{db_path}")

            # Run the migration
            result = perform_json_to_sqlmodel_migration(str(db_path))

            # Migration should succeed
            assert result is True

            # Verify the results
            with Session(db_models.engine) as session:
                # User should be migrated
                users = session.exec(select(UserDB)).all()
                assert len(users) == 1

                # Only valid device should be migrated
                devices = session.exec(select(DeviceDB)).all()
                assert len(devices) == 1
                device = devices[0]
                assert device.id == "valid_device"

        finally:
            db_models.engine = original_engine
