"""Data models for Tronbyt Server users."""

from typing import Any, Literal

from pydantic import BaseModel

from tronbyt_server.models.device import Device


class User(BaseModel):
    """Pydantic model for a user."""

    username: str
    password: str
    email: str = ""
    devices: dict[str, Device] = {}
    api_key: str
    theme_preference: Literal["light", "dark", "system"] = "system"
    system_repo_url: str = ""
    app_repo_url: str = ""

    def to_dict(self) -> dict[str, Any]:
        """Convert the User object to a dictionary."""
        return self.model_dump()

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "User":
        """Create a User instance from a dictionary."""
        return cls(**data)
