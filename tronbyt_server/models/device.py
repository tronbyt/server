"""Data models and validation functions for devices in Tronbyt Server."""

from datetime import datetime
from enum import Enum
from typing import Annotated, Any, Self
from zoneinfo import ZoneInfo
import functools

from pydantic import (
    BaseModel,
    Field,
    AfterValidator,
    BeforeValidator,
    AliasChoices,
    GetCoreSchemaHandler,
)
from pydantic_core import core_schema

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


@functools.total_ordering
class Brightness:
    """A type for representing brightness, handling conversions between percentage, 8-bit, and UI scale."""

    def __init__(self, value: int):
        if not 0 <= value <= 100:
            raise ValueError("Brightness must be a percentage between 0 and 100")
        self.value = value

    @property
    def as_percent(self) -> int:
        """Return brightness as a percentage (0-100)."""
        return self.value

    @property
    def as_8bit(self) -> int:
        """Return brightness as an 8-bit value (0-255)."""
        return (self.value * 255 + 50) // 100

    @property
    def as_ui_scale(self) -> int:
        """Return brightness on a UI scale (0-5)."""
        if self.value == 0:
            return 0
        elif self.value <= 3:
            return 1
        elif self.value <= 5:
            return 2
        elif self.value <= 12:
            return 3
        elif self.value <= 35:
            return 4
        else:
            return 5

    @classmethod
    def from_ui_scale(
        cls, ui_value: int, custom_scale: dict[int, int] | None = None
    ) -> Self:
        """Create a Brightness object from a UI scale value (0-5).

        Args:
            ui_value: The UI scale value (0-5)
            custom_scale: Optional custom brightness scale mapping (0-5 to percentage)

        Returns:
            A Brightness object with the appropriate percentage value
        """
        if custom_scale is not None:
            # Use custom scale if provided
            percent = custom_scale.get(ui_value, 20)  # Default to 20%
        else:
            # Use default scale
            lookup = {
                0: 0,
                1: 3,
                2: 5,
                3: 12,
                4: 35,
                5: 100,
            }
            percent = lookup.get(ui_value, 20)  # Default to 20%
        return cls(percent)

    def __int__(self) -> int:
        return self.value

    def __repr__(self) -> str:
        return f"Brightness({self.value}%)"

    def __eq__(self, other: object) -> bool:
        if isinstance(other, Brightness):
            return self.value == other.value
        if isinstance(other, int):
            return self.value == other
        return NotImplemented

    def __lt__(self, other: object) -> bool:
        if isinstance(other, Brightness):
            return self.value < other.value
        if isinstance(other, int):
            return self.value < other
        return NotImplemented

    @classmethod
    def __get_pydantic_core_schema__(
        cls, source_type: Any, handler: GetCoreSchemaHandler
    ) -> core_schema.CoreSchema:
        """Pydantic custom schema for validation and serialization."""
        from_int_schema = core_schema.chain_schema(
            [
                core_schema.int_schema(ge=0, le=100),
                core_schema.no_info_plain_validator_function(cls),
            ]
        )

        return core_schema.json_or_python_schema(
            json_schema=from_int_schema,
            python_schema=core_schema.union_schema(
                [
                    core_schema.is_instance_schema(cls),
                    from_int_schema,
                ]
            ),
            serialization=core_schema.plain_serializer_function_ser_schema(
                lambda instance: instance.value
            ),
        )


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


def parse_custom_brightness_scale(scale_str: str) -> dict[int, int] | None:
    """Parse custom brightness scale string into a dictionary.

    Args:
        scale_str: Comma-separated values like "0,3,5,12,35,100"

    Returns:
        Dictionary mapping 0-5 to percentage values, or None if invalid
    """
    if not scale_str or not scale_str.strip():
        return None

    try:
        values = [int(v.strip()) for v in scale_str.split(",")]
        if len(values) != 6:
            return None

        # Validate all values are between 0-100
        if not all(0 <= v <= 100 for v in values):
            return None

        # Create mapping
        return {i: values[i] for i in range(6)}
    except (ValueError, AttributeError):
        return None


class Device(BaseModel):
    """Pydantic model for a device."""

    id: DeviceID
    name: str = ""
    type: DeviceType = DEFAULT_DEVICE_TYPE
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""
    brightness: Brightness = Brightness(100)
    custom_brightness_scale: str = ""  # Format: "0,3,5,12,35,100" for levels 0-5
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: Annotated[str | None, BeforeValidator(format_time)] = (
        None  # Time in HH:MM format or legacy int (hour only)
    )
    night_end: Annotated[str | None, BeforeValidator(format_time)] = (
        None  # Time in HH:MM format or legacy int (hour only)
    )
    night_brightness: Brightness = Brightness(0)
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
    info: DeviceInfo = Field(default_factory=DeviceInfo)

    def supports_2x(self) -> bool:
        """
        Check if the device supports 2x apps.

        :return: True if the device supports 2x apps, False otherwise.
        """
        return self.type in TWO_X_CAPABLE_DEVICE_TYPES
