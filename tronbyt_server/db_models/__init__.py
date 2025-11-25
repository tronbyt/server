"""SQLModel database models for Tronbyt Server.

This package contains the SQLModel versions of the data models,
which will replace the JSON-based storage system.
"""

from .database import create_db_and_tables, engine, get_session
from .models import (
    AppDB,
    DeviceDB,
    LocationDB,
    SystemSettingsDB,
    UserDB,
    brightness_from_percent,
    brightness_to_percent,
    seconds_to_timedelta,
    timedelta_to_seconds,
)
from .operations import (
    # User operations
    get_user_by_username,
    get_user_by_api_key,
    get_all_users_db,
    has_users,
    create_user,
    update_user,
    delete_user,
    # Device operations
    get_device_by_id,
    get_devices_by_user_id,
    get_user_by_device_id,
    create_device,
    update_device,
    delete_device,
    update_device_last_seen,
    # App operations
    get_app_by_id,
    get_app_by_device_and_iname,
    get_apps_by_device,
    create_app,
    update_app,
    delete_app,
    delete_app_by_iname,
    # Location operations
    get_location_by_device,
    create_location,
    update_location,
    delete_location,
    # System settings
    get_system_settings,
    update_system_settings,
    # Conversion helpers
    load_user_full,
    load_device_full,
    load_app_full,
    user_db_to_model,
    device_db_to_model,
    app_db_to_model,
    # Save helpers
    save_user_full,
    save_device_full,
    save_app_full,
)

__all__ = [
    # Database
    "create_db_and_tables",
    "engine",
    "get_session",
    # Models
    "SystemSettingsDB",
    "UserDB",
    "DeviceDB",
    "LocationDB",
    "AppDB",
    # Helpers
    "brightness_to_percent",
    "brightness_from_percent",
    "timedelta_to_seconds",
    "seconds_to_timedelta",
    # Operations - User
    "get_user_by_username",
    "get_user_by_api_key",
    "get_all_users_db",
    "has_users",
    "create_user",
    "update_user",
    "delete_user",
    # Operations - Device
    "get_device_by_id",
    "get_devices_by_user_id",
    "get_user_by_device_id",
    "create_device",
    "update_device",
    "delete_device",
    "update_device_last_seen",
    # Operations - App
    "get_app_by_id",
    "get_app_by_device_and_iname",
    "get_apps_by_device",
    "create_app",
    "update_app",
    "delete_app",
    "delete_app_by_iname",
    # Operations - Location
    "get_location_by_device",
    "create_location",
    "update_location",
    "delete_location",
    # Operations - System
    "get_system_settings",
    "update_system_settings",
    # Conversion helpers
    "load_user_full",
    "load_device_full",
    "load_app_full",
    "user_db_to_model",
    "device_db_to_model",
    "app_db_to_model",
    # Save helpers
    "save_user_full",
    "save_device_full",
    "save_app_full",
]
