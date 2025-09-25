"""Data models for Tronbyt Server users."""

from typing import Dict, Literal

from pydantic import BaseModel

from tronbyt_server.models.device import Device


class User(BaseModel):
    """Pydantic model for a user."""

    username: str
    password: str
    email: str = ""
    devices: Dict[str, Device] = {}
    api_key: str
    theme_preference: Literal["light", "dark", "system"] = "system"