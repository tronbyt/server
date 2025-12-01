"""SQLModel for device."""

from datetime import datetime
from typing import Any, ClassVar, Optional, TYPE_CHECKING
from sqlalchemy import Column, Enum as SAEnum, JSON
from sqlmodel import Field, Relationship, SQLModel

from tronbyt_server.models.device import (
    DEFAULT_DEVICE_TYPE,
    DeviceType,
    Brightness as BrightnessModel,
)
from .types import Brightness

if TYPE_CHECKING:
    from .user import UserDB
    from .location import LocationDB
    from .app import AppDB


class DeviceDB(SQLModel, table=True):
    """SQLModel for Device table."""

    __tablename__: ClassVar[Any] = "devices"

    id: str = Field(primary_key=True)
    name: str = ""
    type: DeviceType = Field(
        default=DEFAULT_DEVICE_TYPE,
        sa_column=Column(SAEnum(DeviceType, native_enum=False, length=50)),
    )
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""

    # Brightness fields (using custom Brightness type)
    brightness: BrightnessModel = Field(
        default=BrightnessModel(100), sa_column=Column(Brightness)
    )
    custom_brightness_scale: str = ""
    night_brightness: BrightnessModel = Field(
        default=BrightnessModel(0), sa_column=Column(Brightness)
    )
    dim_brightness: Optional[BrightnessModel] = Field(
        default=None, sa_column=Column(Brightness)
    )

    # Night mode settings (times stored as strings in HH:MM format)
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: Optional[str] = None  # HH:MM format
    night_end: Optional[str] = None  # HH:MM format
    dim_time: Optional[str] = None  # HH:MM format

    # Other settings
    default_interval: int = Field(15, ge=0)
    timezone: Optional[str] = None
    last_app_index: int = 0
    pinned_app: Optional[str] = None
    interstitial_enabled: bool = False
    interstitial_app: Optional[str] = None
    last_seen: Optional[datetime] = None

    # Device info (stored as JSON)
    info: dict[str, Any] = Field(default_factory=dict, sa_column=Column(JSON))

    # Foreign key
    user_id: int = Field(foreign_key="users.id", index=True)

    # Relationships
    user: "UserDB" = Relationship(back_populates="devices")
    location: Optional["LocationDB"] = Relationship(
        back_populates="device",
        sa_relationship_kwargs={"cascade": "all, delete-orphan"},
    )
    apps: list["AppDB"] = Relationship(
        back_populates="device",
        sa_relationship_kwargs={"cascade": "all, delete-orphan"},
    )
