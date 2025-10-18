"""Data models and validation functions for devices in Tronbyt Server."""

from typing import Annotated, Any, Literal
from zoneinfo import ZoneInfo

from pydantic import (
    BaseModel,
    Field,
    AfterValidator,
    BeforeValidator,
)

from .app import App

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
Brightness = Annotated[
    int,
    Field(ge=0, le=100, description="Percentage-based brightness (0-100)"),
]


def validate_timezone(tz: str | None) -> str | None:
    """
    Validate if a timezone string is valid.

    Args:
        tz (str | None): The timezone string to validate.

    Returns:
        str | None: The timezone string if it is valid.
    """
    if not tz:
        return tz
    try:
        ZoneInfo(tz)
        return tz
    except Exception:
        return None


def format_time(v: Any) -> str | None:
    """
    Format time from int to HH:MM string.

    Args:
        v (Any): The value to format.

    Returns:
        Optional[str]: The formatted time string or None.
    """
    if isinstance(v, int):
        return f"{v:02d}:00"
    if isinstance(v, str):
        return v
    return None


class Location(BaseModel):
    """Pydantic model for a location."""

    name: Annotated[
        str | None,
        Field(
            description="Deprecated: kept for backward compatibility", deprecated=True
        ),
    ] = None
    locality: str = ""
    description: str = ""
    place_id: str = ""
    timezone: Annotated[str | None, AfterValidator(validate_timezone)] = None
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
    brightness: Brightness = 100
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: Annotated[str | None, BeforeValidator(format_time)] = (
        None  # Time in HH:MM format or legacy int (hour only)
    )
    night_end: Annotated[str | None, BeforeValidator(format_time)] = (
        None  # Time in HH:MM format or legacy int (hour only)
    )
    night_brightness: Brightness = 0
    dim_time: str | None = None  # Time in HH:MM format when dimming should start
    dim_brightness: Brightness | None = None
    default_interval: int = Field(
        15, ge=0, description="Default interval in minutes (>= 0)"
    )
    timezone: Annotated[str | None, AfterValidator(validate_timezone)] = None
    location: Location | None = None
    apps: dict[str, App] = {}
    last_app_index: int = 0
    pinned_app: str | None = None  # iname of the pinned app, if any

    def supports_2x(self) -> bool:
        """
        Check if the device supports 2x apps.

        :return: True if the device supports 2x apps, False otherwise.
        """
        return self.type in TWO_X_CAPABLE_DEVICE_TYPES
