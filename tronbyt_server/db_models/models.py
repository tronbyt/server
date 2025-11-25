"""SQLModel database models for Tronbyt Server.

These models represent the relational database schema.
They will eventually replace the JSON-based storage in db.py.
"""

from datetime import date, datetime, time, timedelta
from enum import Enum
from typing import Any, Optional

from sqlmodel import Column, Field, JSON, Relationship, SQLModel

# Re-use enums from existing models
from tronbyt_server.models.app import RecurrenceType, Weekday
from tronbyt_server.models.device import (
    Brightness,
    DeviceType,
    ProtocolType,
    DEFAULT_DEVICE_TYPE,
)
from tronbyt_server.models.user import ThemePreference


# ============================================================================
# System Settings
# ============================================================================


class SystemSettingsDB(SQLModel, table=True):
    """SQLModel for system-wide settings (singleton table)."""

    __tablename__ = "system_settings"  # type: ignore

    id: int = Field(default=1, primary_key=True)  # Always 1 (singleton)
    system_repo_url: str = ""


# ============================================================================
# User Models
# ============================================================================


class UserDB(SQLModel, table=True):
    """SQLModel for User table."""

    __tablename__ = "users"  # type: ignore

    id: Optional[int] = Field(default=None, primary_key=True)
    username: str = Field(unique=True, index=True)
    password: str
    email: str = ""
    api_key: str = Field(default="", index=True)
    theme_preference: str = ThemePreference.SYSTEM.value
    app_repo_url: str = ""

    # Relationships
    devices: list["DeviceDB"] = Relationship(
        back_populates="user",
        sa_relationship_kwargs={"cascade": "all, delete-orphan"}
    )


# ============================================================================
# Device Models
# ============================================================================


class LocationDB(SQLModel, table=True):
    """SQLModel for Location table."""

    __tablename__ = "locations"  # type: ignore

    id: Optional[int] = Field(default=None, primary_key=True)
    locality: str = ""
    description: str = ""
    place_id: str = ""
    timezone: Optional[str] = None
    lat: float
    lng: float

    # Foreign key
    device_id: str = Field(foreign_key="devices.id")

    # Relationship
    device: "DeviceDB" = Relationship(back_populates="location")


class DeviceDB(SQLModel, table=True):
    """SQLModel for Device table."""

    __tablename__ = "devices"  # type: ignore

    id: str = Field(primary_key=True)
    name: str = ""
    type: str = DEFAULT_DEVICE_TYPE.value
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""

    # Brightness fields (stored as integers 0-100)
    brightness: int = 100
    custom_brightness_scale: str = ""
    night_brightness: int = 0
    dim_brightness: Optional[int] = None

    # Night mode settings
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: Optional[str] = None  # HH:MM format
    night_end: Optional[str] = None  # HH:MM format
    dim_time: Optional[str] = None  # HH:MM format

    # Other settings
    default_interval: int = Field(15, ge=0)
    timezone: Optional[str] = None
    last_app_index: int = 0
    pinned_app: Optional[str] = None
    interstitial_enabled: bool = False
    interstitial_app: Optional[str] = None
    last_seen: Optional[datetime] = None

    # Device info (stored as JSON)
    info: dict[str, Any] = Field(default_factory=dict, sa_column=Column(JSON))

    # Foreign key
    user_id: int = Field(foreign_key="users.id", index=True)

    # Relationships
    user: UserDB = Relationship(back_populates="devices")
    location: Optional["LocationDB"] = Relationship(
        back_populates="device",
        sa_relationship_kwargs={"cascade": "all, delete-orphan"}
    )
    apps: list["AppDB"] = Relationship(
        back_populates="device",
        sa_relationship_kwargs={"cascade": "all, delete-orphan"}
    )


# ============================================================================
# App Models
# ============================================================================


class AppDB(SQLModel, table=True):
    """SQLModel for App table."""

    __tablename__ = "apps"  # type: ignore

    id: Optional[int] = Field(default=None, primary_key=True)
    iname: str = Field(index=True)
    name: str
    uinterval: int = 0
    display_time: int = 0
    notes: str = ""
    enabled: bool = True
    pushed: bool = False
    order: int = 0
    last_render: int = 0
    last_render_duration: int = 0  # Store as seconds, convert to timedelta in code
    path: Optional[str] = None

    # Time scheduling
    start_time: Optional[str] = None  # Store as HH:MM string
    end_time: Optional[str] = None  # Store as HH:MM string
    days: list[str] = Field(default_factory=list, sa_column=Column(JSON))

    # Custom recurrence
    use_custom_recurrence: bool = False
    recurrence_type: str = RecurrenceType.DAILY.value
    recurrence_interval: int = 1
    recurrence_start_date: Optional[date] = None
    recurrence_end_date: Optional[date] = None

    # App configuration and state
    config: dict[str, Any] = Field(default_factory=dict, sa_column=Column(JSON))
    empty_last_render: bool = False
    render_messages: list[str] = Field(default_factory=list, sa_column=Column(JSON))
    autopin: bool = False
    recurrence_pattern: Optional[dict[str, Any]] = Field(default=None, sa_column=Column(JSON))

    # Foreign key
    device_id: str = Field(foreign_key="devices.id", index=True)

    # Relationships
    device: DeviceDB = Relationship(back_populates="apps")


# ============================================================================
# Helper functions for conversion
# ============================================================================


def brightness_to_percent(brightness: int) -> Brightness:
    """Convert integer brightness to Brightness object."""
    return Brightness(brightness)


def brightness_from_percent(brightness: Brightness) -> int:
    """Convert Brightness object to integer percentage."""
    return brightness.as_percent


def timedelta_to_seconds(td: timedelta) -> int:
    """Convert timedelta to total seconds."""
    return int(td.total_seconds())


def seconds_to_timedelta(seconds: int) -> timedelta:
    """Convert seconds to timedelta."""
    return timedelta(seconds=seconds)
