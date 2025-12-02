"""SQLModel database operations for Tronbyt Server.

This module provides CRUD operations using SQLModel instead of JSON-based storage.
These functions replace the JSON manipulation in db.py with proper relational queries.
"""

import logging
from datetime import date, datetime, time as dt_time
from typing import Any

from sqlmodel import Session, select

from tronbyt_server.db_models.models import (
    AppDB,
    DeviceDB,
    LocationDB,
    SystemSettingsDB,
    UserDB,
)
from tronbyt_server.models import App, Device, Location, RecurrencePattern, User
from tronbyt_server.models.device import DeviceInfo

logger = logging.getLogger(__name__)


# ============================================================================
# User Operations
# ============================================================================


def get_user_by_username(session: Session, username: str) -> UserDB | None:
    """Get a user by username from the database."""
    statement = select(UserDB).where(UserDB.username == username)
    return session.exec(statement).first()


def get_user_by_api_key(session: Session, api_key: str) -> UserDB | None:
    """Get a user by API key."""
    statement = select(UserDB).where(UserDB.api_key == api_key)
    return session.exec(statement).first()


def get_all_users_db(session: Session) -> list[UserDB]:
    """Get all users from the database."""
    statement = select(UserDB)
    return list(session.exec(statement).all())


def has_users(session: Session) -> bool:
    """Check if any users exist in the database."""
    statement = select(UserDB).limit(1)
    return session.exec(statement).first() is not None


def create_user(
    session: Session, username: str, password: str, email: str = "", api_key: str = ""
) -> UserDB:
    """Create a new user in the database."""
    user = UserDB(
        username=username,
        password=password,
        email=email,
        api_key=api_key,
    )
    session.add(user)
    session.commit()
    session.refresh(user)
    return user


def update_user(session: Session, user: UserDB) -> UserDB:
    """Update an existing user in the database."""
    session.add(user)
    session.commit()
    session.refresh(user)
    return user


def save_user_full(session: Session, user: "User", new_user: bool = False) -> UserDB:
    """Save a complete User with all devices, apps, locations, etc."""
    # Create or update the user record
    user_db: UserDB
    if new_user:
        user_db = create_user(
            session, user.username, user.password, user.email, user.api_key
        )
    else:
        user_db_maybe = get_user_by_username(session, user.username)
        if not user_db_maybe:
            raise ValueError(f"User {user.username} not found")
        user_db = user_db_maybe

        # Update user fields (no conversion needed - native enum support)
        user_db.password = user.password
        user_db.email = user.email
        user_db.api_key = user.api_key
        user_db.theme_preference = user.theme_preference
        user_db.app_repo_url = user.app_repo_url
        session.add(user_db)
        session.commit()
        session.refresh(user_db)

    # Now save all devices
    assert user_db.id is not None, "User ID should be set after database save"
    existing_device_ids = {d.id for d in get_devices_by_user_id(session, user_db.id)}
    current_device_ids = set(user.devices.keys())

    # Delete devices that no longer exist
    for device_id in existing_device_ids - current_device_ids:
        delete_device(session, device_id)

    # Create or update devices
    for device_id, device in user.devices.items():
        save_device_full(session, user_db.id, device)

    return user_db


def save_device_full(session: Session, user_id: int, device: "Device") -> DeviceDB:
    """Save a complete Device with all apps and location."""
    # Check if device exists
    device_db = get_device_by_id(session, device.id)

    # Helper to convert time to string
    def time_to_str(t: dt_time | str | None) -> str | None:
        if t is None:
            return None
        if isinstance(t, str):
            return t  # Already a string
        return f"{t.hour:02d}:{t.minute:02d}"

    # Convert device.info to dict if it's a Pydantic model
    device_info_obj = device.info
    device_info_dict: dict[str, Any]
    if hasattr(device_info_obj, "model_dump"):
        device_info_dict = device_info_obj.model_dump()
    elif isinstance(device_info_obj, dict):
        device_info_dict = device_info_obj
    else:
        device_info_dict = {}

    # No conversion needed - DB models now handle native types
    device_data = {
        "id": device.id,
        "name": device.name,
        "type": device.type,  # Native enum support
        "api_key": device.api_key,
        "img_url": device.img_url,
        "ws_url": device.ws_url,
        "notes": device.notes,
        "brightness": device.brightness,  # Custom Brightness type handles conversion
        "custom_brightness_scale": device.custom_brightness_scale,
        "night_brightness": device.night_brightness,  # Custom Brightness type
        "dim_brightness": device.dim_brightness,  # Custom Brightness type (nullable)
        "night_mode_enabled": device.night_mode_enabled,
        "night_mode_app": device.night_mode_app,
        "night_start": time_to_str(
            device.night_start
        ),  # Keep string conversion for now
        "night_end": time_to_str(device.night_end),
        "dim_time": time_to_str(device.dim_time),
        "default_interval": device.default_interval,
        "timezone": device.timezone,
        "last_app_index": device.last_app_index,
        "pinned_app": device.pinned_app,
        "interstitial_enabled": device.interstitial_enabled,
        "interstitial_app": device.interstitial_app,
        "last_seen": device.last_seen,
        "info": device_info_dict,
        "user_id": user_id,
    }

    if device_db:
        # Update existing device
        for key, value in device_data.items():
            if key != "id":  # Don't update the primary key
                setattr(device_db, key, value)
        session.add(device_db)
    else:
        # Create new device
        device_db = DeviceDB(**device_data)
        session.add(device_db)

    session.commit()
    session.refresh(device_db)

    # Save location if it exists
    if device.location:
        location_db = get_location_by_device(session, device.id)
        location_data = {
            "device_id": device.id,
            "locality": device.location.locality,
            "description": device.location.description,
            "place_id": device.location.place_id,
            "timezone": device.location.timezone,
            "lat": device.location.lat,
            "lng": device.location.lng,
        }
        if location_db:
            for key, value in location_data.items():
                if key != "id":
                    setattr(location_db, key, value)
            session.add(location_db)
        else:
            location_db = LocationDB(**location_data)
            session.add(location_db)
        session.commit()
    else:
        # Delete location if it exists but shouldn't
        delete_location(session, device.id)

    # Save all apps
    existing_app_inames = {a.iname for a in get_apps_by_device(session, device.id)}
    current_app_inames = set(device.apps.keys())

    # Delete apps that no longer exist
    for iname in existing_app_inames - current_app_inames:
        delete_app_by_iname(session, device.id, iname)

    # Create or update apps
    for iname, app in device.apps.items():
        save_app_full(session, device.id, app)

    return device_db


def save_app_full(session: Session, device_id: str, app: "App") -> AppDB:
    """Save a complete App with recurrence pattern."""
    # Check if app exists
    app_db = get_app_by_device_and_iname(session, device_id, app.iname)

    # Helper to convert time to string
    def time_to_str(t: dt_time | str | None) -> str | None:
        if t is None:
            return None
        if isinstance(t, str):
            return t  # Already a string
        return f"{t.hour:02d}:{t.minute:02d}"

    # Convert recurrence pattern to dict if it exists
    recurrence_pattern_dict = None
    if app.recurrence_pattern:
        # Convert weekdays to strings (Weekday enums inherit from str, so they can be used directly)
        weekdays_list = []
        if app.recurrence_pattern.weekdays:
            for wd in app.recurrence_pattern.weekdays:
                weekdays_list.append(str(wd))

        recurrence_pattern_dict = {
            "day_of_month": app.recurrence_pattern.day_of_month,
            "day_of_week": app.recurrence_pattern.day_of_week,
            "weekdays": weekdays_list,
        }

    # Convert days to strings (Weekday enums inherit from str, so they can be used directly)
    days_list = []
    for day in app.days:
        days_list.append(str(day))

    # Convert date strings to date objects if needed (keep this for string input)
    recurrence_start_date_obj = None
    if app.recurrence_start_date:
        if isinstance(app.recurrence_start_date, str):
            recurrence_start_date_obj = date.fromisoformat(app.recurrence_start_date)
        else:
            recurrence_start_date_obj = app.recurrence_start_date

    recurrence_end_date_obj = None
    if app.recurrence_end_date:
        if isinstance(app.recurrence_end_date, str):
            recurrence_end_date_obj = date.fromisoformat(app.recurrence_end_date)
        else:
            recurrence_end_date_obj = app.recurrence_end_date

    # No conversion needed for most fields - DB models handle native types
    app_data = {
        "device_id": device_id,
        "iname": app.iname,
        "name": app.name,
        "uinterval": app.uinterval,
        "display_time": app.display_time,
        "notes": app.notes,
        "enabled": app.enabled,
        "pushed": app.pushed,
        "order": app.order,
        "last_render": app.last_render,
        "last_render_duration": app.last_render_duration,  # Native timedelta support
        "path": app.path,
        "start_time": time_to_str(app.start_time),  # Keep string conversion for now
        "end_time": time_to_str(app.end_time),
        "days": days_list,  # Still need to convert Weekday enums to strings for JSON
        "use_custom_recurrence": app.use_custom_recurrence,
        "recurrence_type": app.recurrence_type,  # Native enum support
        "recurrence_interval": app.recurrence_interval,
        "recurrence_start_date": recurrence_start_date_obj,
        "recurrence_end_date": recurrence_end_date_obj,
        "config": app.config,
        "empty_last_render": app.empty_last_render,
        "render_messages": app.render_messages,
        "autopin": app.autopin,
        "recurrence_pattern": recurrence_pattern_dict,
    }

    if app_db:
        # Update existing app
        for key, value in app_data.items():
            if key not in ("id", "device_id"):
                setattr(app_db, key, value)
        session.add(app_db)
    else:
        # Create new app
        app_db = AppDB(**app_data)
        session.add(app_db)

    session.commit()
    session.refresh(app_db)

    # Recurrence pattern is now stored as JSON on the app itself
    # No need for separate operations
    return app_db


def delete_user(session: Session, username: str) -> bool:
    """Delete a user and all their devices from the database."""
    user = get_user_by_username(session, username)
    if not user:
        return False

    # SQLModel relationships handle cascading deletes if configured
    # For now, manually delete devices (which will cascade to apps, locations, etc.)
    statement = select(DeviceDB).where(DeviceDB.user_id == user.id)
    devices = session.exec(statement).all()
    for device in devices:
        session.delete(device)

    session.delete(user)
    session.commit()
    return True


# ============================================================================
# Device Operations
# ============================================================================


def get_device_by_id(session: Session, device_id: str) -> DeviceDB | None:
    """Get a device by ID."""
    statement = select(DeviceDB).where(DeviceDB.id == device_id)
    return session.exec(statement).first()


def get_devices_by_user_id(session: Session, user_id: int) -> list[DeviceDB]:
    """Get all devices for a user."""
    statement = select(DeviceDB).where(DeviceDB.user_id == user_id)
    return list(session.exec(statement).all())


def get_user_by_device_id(session: Session, device_id: str) -> UserDB | None:
    """Get the user who owns a device."""
    device = get_device_by_id(session, device_id)
    if device:
        return session.get(UserDB, device.user_id)
    return None


def create_device(
    session: Session, user_id: int, device_data: dict[str, Any]
) -> DeviceDB:
    """Create a new device for a user."""
    device = DeviceDB(user_id=user_id, **device_data)
    session.add(device)
    session.commit()
    session.refresh(device)
    return device


def update_device(session: Session, device: DeviceDB) -> DeviceDB:
    """Update an existing device."""
    session.add(device)
    session.commit()
    session.refresh(device)
    return device


def delete_device(session: Session, device_id: str) -> bool:
    """Delete a device and all its apps and location."""
    device = get_device_by_id(session, device_id)
    if not device:
        return False

    session.delete(device)
    session.commit()
    return True


def update_device_last_seen(session: Session, device_id: str) -> None:
    """Update the last_seen timestamp for a device."""
    device = get_device_by_id(session, device_id)
    if device:
        device.last_seen = datetime.now()
        session.add(device)
        session.commit()


# ============================================================================
# App Operations
# ============================================================================


def get_app_by_id(session: Session, app_id: int) -> AppDB | None:
    """Get an app by ID."""
    return session.get(AppDB, app_id)


def get_app_by_device_and_iname(
    session: Session, device_id: str, iname: str
) -> AppDB | None:
    """Get an app by device ID and installation name."""
    statement = select(AppDB).where(AppDB.device_id == device_id, AppDB.iname == iname)
    return session.exec(statement).first()


def get_apps_by_device(session: Session, device_id: str) -> list[AppDB]:
    """Get all apps for a device."""
    statement = (
        select(AppDB).where(AppDB.device_id == device_id).order_by(AppDB.order.asc())  # type: ignore[attr-defined]
    )
    return list(session.exec(statement).all())


def create_app(session: Session, device_id: str, app_data: dict[str, Any]) -> AppDB:
    """Create a new app for a device."""
    app = AppDB(device_id=device_id, **app_data)
    session.add(app)
    session.commit()
    session.refresh(app)
    return app


def update_app(session: Session, app: AppDB) -> AppDB:
    """Update an existing app."""
    session.add(app)
    session.commit()
    session.refresh(app)
    return app


def delete_app(session: Session, app_id: int) -> bool:
    """Delete an app."""
    app = get_app_by_id(session, app_id)
    if not app:
        return False

    session.delete(app)
    session.commit()
    return True


def delete_app_by_iname(session: Session, device_id: str, iname: str) -> bool:
    """Delete an app by device ID and installation name."""
    app = get_app_by_device_and_iname(session, device_id, iname)
    if not app:
        return False

    session.delete(app)
    session.commit()
    return True


# ============================================================================
# Location Operations
# ============================================================================


def get_location_by_device(session: Session, device_id: str) -> LocationDB | None:
    """Get the location for a device."""
    statement = select(LocationDB).where(LocationDB.device_id == device_id)
    return session.exec(statement).first()


def create_location(
    session: Session, device_id: str, location_data: dict[str, Any]
) -> LocationDB:
    """Create a location for a device."""
    location = LocationDB(device_id=device_id, **location_data)
    session.add(location)
    session.commit()
    session.refresh(location)
    return location


def update_location(session: Session, location: LocationDB) -> LocationDB:
    """Update an existing location."""
    session.add(location)
    session.commit()
    session.refresh(location)
    return location


def delete_location(session: Session, device_id: str) -> bool:
    """Delete the location for a device."""
    location = get_location_by_device(session, device_id)
    if not location:
        return False

    session.delete(location)
    session.commit()
    return True


# ============================================================================
# System Settings Operations
# ============================================================================


def get_system_settings(session: Session) -> SystemSettingsDB:
    """Get the system settings (singleton)."""
    settings = session.get(SystemSettingsDB, 1)
    if not settings:
        # Create default settings if they don't exist
        settings = SystemSettingsDB(id=1, system_repo_url="")
        session.add(settings)
        session.commit()
        session.refresh(settings)
    return settings


def update_system_settings(
    session: Session, settings: SystemSettingsDB
) -> SystemSettingsDB:
    """Update system settings."""
    session.add(settings)
    session.commit()
    session.refresh(settings)
    return settings


# ============================================================================
# Conversion Helpers (DB models <-> Pydantic models)
# ============================================================================


def load_user_full(session: Session, user_db: UserDB) -> User:
    """Load a complete User with all devices and apps."""
    # Load all devices for this user
    assert user_db.id is not None, "User ID should be set"
    devices_db = get_devices_by_user_id(session, user_db.id)

    # Convert devices to dict format, loading apps for each
    devices_dict = {}
    for device_db in devices_db:
        device_model = load_device_full(session, device_db)
        devices_dict[device_db.id] = device_model

    # No conversion needed - DB already has native enum
    return User(
        username=user_db.username,
        password=user_db.password,
        email=user_db.email,
        api_key=user_db.api_key,
        theme_preference=user_db.theme_preference,  # Already a ThemePreference enum
        app_repo_url=user_db.app_repo_url,
        devices=devices_dict,
    )


def load_device_full(session: Session, device_db: DeviceDB) -> Device:
    """Load a complete Device with all apps and location."""
    # Load all apps for this device
    apps_db = get_apps_by_device(session, device_db.id)

    # Convert apps to dict format
    apps_dict = {}
    for app_db in apps_db:
        app_model = load_app_full(session, app_db)
        apps_dict[app_db.iname] = app_model

    # Load location if it exists
    location = None
    location_db = get_location_by_device(session, device_db.id)
    if location_db:
        location = Location(
            locality=location_db.locality,
            description=location_db.description,
            place_id=location_db.place_id,
            timezone=location_db.timezone,
            lat=location_db.lat,
            lng=location_db.lng,
        )

    # No conversion needed - DB models already have correct types
    return Device(
        id=device_db.id,
        name=device_db.name,
        type=device_db.type,  # Already DeviceType enum
        api_key=device_db.api_key,
        img_url=device_db.img_url,
        ws_url=device_db.ws_url,
        notes=device_db.notes,
        brightness=device_db.brightness,  # Already Brightness object
        custom_brightness_scale=device_db.custom_brightness_scale,
        night_brightness=device_db.night_brightness,  # Already Brightness object
        dim_brightness=device_db.dim_brightness,  # Already Brightness object or None
        night_mode_enabled=device_db.night_mode_enabled,
        night_mode_app=device_db.night_mode_app,
        night_start=device_db.night_start,
        night_end=device_db.night_end,
        dim_time=device_db.dim_time,
        default_interval=device_db.default_interval,
        timezone=device_db.timezone,
        last_app_index=device_db.last_app_index,
        pinned_app=device_db.pinned_app,
        interstitial_enabled=device_db.interstitial_enabled,
        interstitial_app=device_db.interstitial_app,
        last_seen=device_db.last_seen,
        info=DeviceInfo(**device_db.info) if device_db.info else DeviceInfo(),
        location=location,
        apps=apps_dict,
    )


def load_app_full(session: Session, app_db: AppDB) -> App:
    """Load a complete App with recurrence pattern."""
    from tronbyt_server.models.app import Weekday

    # Convert days list to Weekday enums (still needed for JSON field)
    days = [Weekday(day) for day in app_db.days] if app_db.days else []

    # No conversion needed for most fields - DB models already have correct types
    app_kwargs = {
        "id": str(app_db.id),
        "iname": app_db.iname,
        "name": app_db.name,
        "uinterval": app_db.uinterval,
        "display_time": app_db.display_time,
        "notes": app_db.notes,
        "enabled": app_db.enabled,
        "pushed": app_db.pushed,
        "order": app_db.order,
        "last_render": app_db.last_render,
        "last_render_duration": app_db.last_render_duration,  # Already timedelta
        "path": app_db.path,
        "start_time": app_db.start_time,
        "end_time": app_db.end_time,
        "days": days,
        "use_custom_recurrence": app_db.use_custom_recurrence,
        "recurrence_type": app_db.recurrence_type,  # Already RecurrenceType enum
        "recurrence_interval": app_db.recurrence_interval,
        "recurrence_start_date": app_db.recurrence_start_date,
        "recurrence_end_date": app_db.recurrence_end_date,
        "config": app_db.config,
        "empty_last_render": app_db.empty_last_render,
        "render_messages": app_db.render_messages,
        "autopin": app_db.autopin,
    }

    # Only add recurrence_pattern if it exists (otherwise let App use its default factory)
    if app_db.recurrence_pattern:
        rp = app_db.recurrence_pattern
        weekdays = [Weekday(wd) for wd in rp.get("weekdays", [])]
        app_kwargs["recurrence_pattern"] = RecurrencePattern(
            day_of_month=rp.get("day_of_month"),
            day_of_week=rp.get("day_of_week"),
            weekdays=weekdays,
        )

    return App(**app_kwargs)  # type: ignore[arg-type]


def user_db_to_model(user_db: UserDB, devices: list[DeviceDB]) -> User:
    """Convert a UserDB to a User Pydantic model (without loading related data).

    Use load_user_full() instead if you need devices and apps loaded.
    """
    # Convert devices to dict format
    devices_dict = {}
    for device_db in devices:
        device_model = device_db_to_model(device_db)
        devices_dict[device_db.id] = device_model

    # No conversion needed - DB already has native types
    return User(
        username=user_db.username,
        password=user_db.password,
        email=user_db.email,
        api_key=user_db.api_key,
        theme_preference=user_db.theme_preference,  # Already ThemePreference enum
        app_repo_url=user_db.app_repo_url,
        devices=devices_dict,
    )


def device_db_to_model(device_db: DeviceDB) -> Device:
    """Convert a DeviceDB to a Device Pydantic model."""
    # Convert apps (would need session to load)
    # This is a placeholder - in practice, you'd pass apps or load them separately
    apps_dict: dict[str, App] = {}

    # Convert location if it exists
    location = None
    if device_db.location:
        location = Location(
            locality=device_db.location.locality,
            description=device_db.location.description,
            place_id=device_db.location.place_id,
            timezone=device_db.location.timezone,
            lat=device_db.location.lat,
            lng=device_db.location.lng,
        )

    # No conversion needed - DB models already have correct types
    return Device(
        id=device_db.id,
        name=device_db.name,
        type=device_db.type,  # Already DeviceType enum
        api_key=device_db.api_key,
        img_url=device_db.img_url,
        ws_url=device_db.ws_url,
        notes=device_db.notes,
        brightness=device_db.brightness,  # Already Brightness object
        custom_brightness_scale=device_db.custom_brightness_scale,
        night_brightness=device_db.night_brightness,  # Already Brightness object
        dim_brightness=device_db.dim_brightness,  # Already Brightness object or None
        night_mode_enabled=device_db.night_mode_enabled,
        night_mode_app=device_db.night_mode_app,
        night_start=device_db.night_start,
        night_end=device_db.night_end,
        dim_time=device_db.dim_time,
        default_interval=device_db.default_interval,
        timezone=device_db.timezone,
        last_app_index=device_db.last_app_index,
        pinned_app=device_db.pinned_app,
        interstitial_enabled=device_db.interstitial_enabled,
        interstitial_app=device_db.interstitial_app,
        last_seen=device_db.last_seen,
        info=DeviceInfo(**device_db.info) if device_db.info else DeviceInfo(),
        location=location,
        apps=apps_dict,  # Will be populated separately
    )


def app_db_to_model(app_db: AppDB) -> App:
    """Convert an AppDB to an App Pydantic model."""
    from tronbyt_server.models.app import Weekday

    # Parse time strings (App model expects time objects or strings)
    start_time = None
    if app_db.start_time:
        try:
            hour, minute = map(int, app_db.start_time.split(":"))
            start_time = dt_time(hour, minute)
        except (ValueError, AttributeError):
            logger.warning(
                "Failed to parse start_time '%s' for AppDB(id=%s). Leaving as None.",
                app_db.start_time,
                getattr(app_db, "id", "<unknown>")
            )

    end_time = None
    if app_db.end_time:
        try:
            hour, minute = map(int, app_db.end_time.split(":"))
            end_time = dt_time(hour, minute)
        except (ValueError, AttributeError):
            logger.warning(
                "Failed to parse end_time '%s' for AppDB(id=%s). Leaving as None.",
                app_db.end_time,
                getattr(app_db, "id", "<unknown>")
            )

    # Convert days list to Weekday enums (still needed for JSON field)
    days = [Weekday(day) for day in app_db.days] if app_db.days else []

    # No conversion needed for most fields - DB models already have correct types
    app_kwargs = {
        "id": str(app_db.id),
        "iname": app_db.iname,
        "name": app_db.name,
        "uinterval": app_db.uinterval,
        "display_time": app_db.display_time,
        "notes": app_db.notes,
        "enabled": app_db.enabled,
        "pushed": app_db.pushed,
        "order": app_db.order,
        "last_render": app_db.last_render,
        "last_render_duration": app_db.last_render_duration,  # Already timedelta
        "path": app_db.path,
        "start_time": start_time,
        "end_time": end_time,
        "days": days,
        "use_custom_recurrence": app_db.use_custom_recurrence,
        "recurrence_type": app_db.recurrence_type,  # Already RecurrenceType enum
        "recurrence_interval": app_db.recurrence_interval,
        "recurrence_start_date": app_db.recurrence_start_date,
        "recurrence_end_date": app_db.recurrence_end_date,
        "config": app_db.config,
        "empty_last_render": app_db.empty_last_render,
        "render_messages": app_db.render_messages,
        "autopin": app_db.autopin,
    }

    # Only add recurrence_pattern if it exists (otherwise let App use its default factory)
    if app_db.recurrence_pattern:
        rp = app_db.recurrence_pattern
        weekdays = [Weekday(wd) for wd in rp.get("weekdays", [])]
        app_kwargs["recurrence_pattern"] = RecurrencePattern(
            day_of_month=rp.get("day_of_month"),
            day_of_week=rp.get("day_of_week"),
            weekdays=weekdays,
        )

    return App(**app_kwargs)  # type: ignore[arg-type]
