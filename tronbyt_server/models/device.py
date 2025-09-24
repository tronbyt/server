"""Data models and validation functions for devices in Tronbyt Server."""

import re
from typing import Any
from zoneinfo import ZoneInfo

from pydantic import BaseModel, field_validator

from tronbyt_server.models.app import App

DEFAULT_DEVICE_TYPE = "tidbyt_gen1"

TWO_X_CAPABLE_DEVICE_TYPES = ["tronbyt_s3_wide"]
DEVICE_TYPES = [
    "tidbyt_gen1",
    "tidbyt_gen2",
    "pixoticker",
    "raspberrypi",
    "tronbyt_s3",
    "tronbyt_s3_wide",
    "other",
]


class Location(BaseModel):
    """Pydantic model for a location."""

    name: str = ""
    timezone: str = ""
    lat: float
    lng: float


class Device(BaseModel):
    """Pydantic model for a device."""

    id: str
    name: str = ""
    type: str = DEFAULT_DEVICE_TYPE
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""
    brightness: int = 100  # Percentage-based brightness (0-100)
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: int = 0
    night_end: int = 0
    night_brightness: int = 0  # Percentage-based night brightness (0-100)
    default_interval: int = 15
    timezone: str = ""
    location: Location | None = None
    apps: dict[str, App] = {}
    last_app_index: int = 0
    pinned_app: str = ""  # iname of the pinned app, if any

    def to_dict(self) -> dict[str, Any]:
        """Convert the Device object to a dictionary."""
        return self.model_dump()

    @classmethod
    def from_dict(cls, data: dict[str, Any]) -> "Device":
        """Create a Device instance from a dictionary."""
        return cls(**data)

    @field_validator("id")
    @classmethod
    def check_device_id(cls, v: str) -> str:
        """Validate device ID."""
        if not validate_device_id(v):
            raise ValueError("Invalid device ID")
        return v

    @field_validator("type")
    @classmethod
    def check_device_type(cls, v: str) -> str:
        """Validate device type."""
        if not validate_device_type(v):
            raise ValueError("Invalid device type")
        return v


def validate_device_id(v: str) -> bool:
    """Validate device ID."""
    return re.match(r"^[a-fA-F0-9]{8}$", v) is not None


def validate_device_type(v: str) -> bool:
    """Validate device type."""
    return v in DEVICE_TYPES


def validate_timezone(tz: str) -> ZoneInfo | None:
    """
    Validate if a timezone string is valid.

    Args:
        tz (str): The timezone string to validate.

    Returns:
        Optional[ZoneInfo]: A ZoneInfo object if the timezone string is valid,
        None otherwise.
    """
    try:
        return ZoneInfo(tz)
    except Exception:
        return None


def device_supports_2x(device: Device) -> bool:
    """
    Check if the device supports 2x apps.

    :param device: The device to check.
    :return: True if the device supports 2x apps, False otherwise.
    """
    return device.type in TWO_X_CAPABLE_DEVICE_TYPES
