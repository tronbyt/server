"""SQLModel database models for Tronbyt Server.

This package contains the SQLModel versions of the data models,
which will replace the JSON-based storage system.
"""

from .database import create_db_and_tables, engine, get_session
from .models import (
    AppDB,
    DeviceDB,
    LocationDB,
    RecurrencePatternDB,
    UserDB,
    brightness_from_percent,
    brightness_to_percent,
    seconds_to_timedelta,
    timedelta_to_seconds,
)

__all__ = [
    # Database
    "create_db_and_tables",
    "engine",
    "get_session",
    # Models
    "UserDB",
    "DeviceDB",
    "LocationDB",
    "AppDB",
    "RecurrencePatternDB",
    # Helpers
    "brightness_to_percent",
    "brightness_from_percent",
    "timedelta_to_seconds",
    "seconds_to_timedelta",
]
