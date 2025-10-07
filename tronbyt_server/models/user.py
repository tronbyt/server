"""Data models for Tronbyt Server users."""

from typing import Literal

from pydantic import BaseModel, Field

from .device import Device


class User(BaseModel):
    """Pydantic model for a user."""

    username: str = Field(pattern=r"^[A-Za-z0-9_-]+$")
    password: str
    email: str = ""
    devices: dict[str, Device] = {}
    api_key: str
    theme_preference: Literal["light", "dark", "system"] = "system"
    system_repo_url: str = ""
    app_repo_url: str = ""
