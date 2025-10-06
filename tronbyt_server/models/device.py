"""Data models and validation functions for devices in Tronbyt Server."""

from typing import Annotated, Literal
from zoneinfo import ZoneInfo

from pydantic import BaseModel, Field

from tronbyt_server.models.app import App

DeviceType = Literal[
    "tidbyt_gen1",
    "tidbyt_gen2",
    "pixoticker",
    "raspberrypi",
    "tronbyt_s3",
    "tronbyt_s3_wide",
    "other",
]
DEFAULT_DEVICE_TYPE: DeviceType = "tidbyt_gen1"

TWO_X_CAPABLE_DEVICE_TYPES = ["tronbyt_s3_wide"]

DeviceID = Annotated[str, Field(pattern=r"^[a-fA-F0-9]{8}$")]


class Location(BaseModel):
    """Pydantic model for a location."""

    name: str = ""
    timezone: str = ""
    lat: float
    lng: float


class Device(BaseModel):
    """Pydantic model for a device."""

    id: DeviceID
    name: str = ""
    type: DeviceType = DEFAULT_DEVICE_TYPE
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""
    brightness: int = Field(
        100, ge=0, le=100, description="Percentage-based brightness (0-100)"
    )
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: int | str | None = (
        None  # Time in HH:MM format or legacy int (hour only)
    )
    night_end: int | str | None = None  # Time in HH:MM format or legacy int (hour only)
    night_brightness: int = Field(
        0, ge=0, le=100, description="Percentage-based night brightness (0-100)"
    )
    dim_time: str | None = None  # Time in HH:MM format when dimming should start
    dim_brightness: int | None = Field(
        None, ge=0, le=100, description="Percentage-based dim brightness (0-100)"
    )
    default_interval: int = Field(
        15, ge=0, description="Default interval in minutes (>= 0)"
    )
    timezone: str = ""
    location: Location | None = None
    apps: dict[str, App] = {}
    last_app_index: int = 0
    pinned_app: str = ""  # iname of the pinned app, if any


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
