"""SQLModel for location."""

from typing import Optional, TYPE_CHECKING
from sqlmodel import Field, Relationship, SQLModel

if TYPE_CHECKING:
    from .device import DeviceDB


class LocationDB(SQLModel, table=True):
    """SQLModel for Location table."""

    __tablename__ = "locations"

    id: Optional[int] = Field(default=None, primary_key=True)
    locality: str = ""
    description: str = ""
    place_id: str = ""
    timezone: Optional[str] = None
    lat: float
    lng: float

    # Foreign key
    device_id: str = Field(foreign_key="devices.id")

    # Relationship
    device: "DeviceDB" = Relationship(back_populates="location")
