"""Data models for Tronbyt Server users."""

from enum import Enum
from pydantic import BaseModel, Field

from .device import Device


class ThemePreference(str, Enum):
    """Theme preference enumeration."""

    LIGHT = "light"
    DARK = "dark"
    SYSTEM = "system"


class User(BaseModel):
    """Pydantic model for a user."""

    username: str = Field(pattern=r"^[A-Za-z0-9_-]+$")
    password: str
    email: str = ""
    devices: dict[str, Device] = {}
    api_key: str = ""
    theme_preference: ThemePreference = ThemePreference.SYSTEM
    system_repo_url: str = ""
    app_repo_url: str = ""
