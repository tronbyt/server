"""SQLModel database models for Tronbyt Server.

This file re-exports all models for backward compatibility.
The actual models are now in separate files.
"""

from sqlmodel import SQLModel

from .app import AppDB
from .device import DeviceDB
from .location import LocationDB
from .system_settings import SystemSettingsDB
from .user import UserDB

# Explicitly export SQLModel for alembic/env.py
__all__ = [
    "SQLModel",
    "SystemSettingsDB",
    "UserDB",
    "DeviceDB",
    "LocationDB",
    "AppDB",
]
