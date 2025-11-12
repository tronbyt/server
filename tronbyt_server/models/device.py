"""Data models and validation functions for devices in Tronbyt Server."""

from datetime import datetime
from enum import Enum
from typing import Annotated, Any
from zoneinfo import ZoneInfo

from pydantic import (
    BaseModel,
    Field,
    AfterValidator,
    BeforeValidator,
    AliasChoices,
)

from .app import App


class DeviceType(str, Enum):
    """Device type enumeration."""

    TIDBYT_GEN1 = "tidbyt_gen1"
    TIDBYT_GEN2 = "tidbyt_gen2"
    PIXOTICKER = "pixoticker"
    RASPBERRYPI = "raspberrypi"
    TRONBYT_S3 = "tronbyt_s3"
    TRONBYT_S3_WIDE = "tronbyt_s3_wide"
    MATRIXPORTAL = "matrixportal_s3"
    MATRIXPORTAL_S3_WAVESHARE = "matrixportal_s3_waveshare"
    OTHER = "other"


DEFAULT_DEVICE_TYPE: DeviceType = DeviceType.TIDBYT_GEN1

TWO_X_CAPABLE_DEVICE_TYPES = [DeviceType.TRONBYT_S3_WIDE]

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
        str | None: The formatted time string or None.
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


class ProtocolType(str, Enum):
    """Protocol type enumeration."""

    HTTP = "HTTP"
    WS = "WS"


class DeviceInfoBase(BaseModel):
    """Pydantic model for device information that is reported by the device."""

    firmware_version: str | None = None
    firmware_type: str | None = None
    protocol_version: int | None = None
    mac_address: str | None = Field(
        default=None, validation_alias=AliasChoices("mac_address", "mac")
    )


class DeviceInfo(DeviceInfoBase):
    """Pydantic model for device information that is reported by the device."""

    protocol_type: ProtocolType | None = None


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
    interstitial_enabled: bool = False  # whether interstitial app feature is enabled
    interstitial_app: str | None = None  # iname of the interstitial app, if any
    last_seen: datetime | None = None
    info: dict[str, Any] = Field(default_factory=dict)

    def supports_2x(self) -> bool:
        """
        Check if the device supports 2x apps.

        :return: True if the device supports 2x apps, False otherwise.
        """
        return self.type in TWO_X_CAPABLE_DEVICE_TYPES
