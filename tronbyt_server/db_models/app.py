"""SQLModel for app."""

from datetime import date, timedelta
from typing import Any, ClassVar, Optional, TYPE_CHECKING
from sqlalchemy import Column, Enum as SAEnum, Interval, JSON
from sqlmodel import Field, Relationship, SQLModel

from tronbyt_server.models.app import RecurrenceType

if TYPE_CHECKING:
    from .device import DeviceDB


class AppDB(SQLModel, table=True):
    """SQLModel for App table."""

    __tablename__: ClassVar[Any] = "apps"

    id: Optional[int] = Field(default=None, primary_key=True)
    iname: str = Field(index=True)
    name: str
    uinterval: int = 0
    display_time: int = 0
    notes: str = ""
    enabled: bool = True
    pushed: bool = False
    order: int = 0
    last_render: int = 0
    last_render_duration: timedelta = Field(
        default=timedelta(seconds=0), sa_column=Column(Interval)
    )
    path: Optional[str] = None

    # Time scheduling (stored as HH:MM strings)
    start_time: Optional[str] = None
    end_time: Optional[str] = None
    days: list[str] = Field(default_factory=list, sa_column=Column(JSON))

    # Custom recurrence
    use_custom_recurrence: bool = False
    recurrence_type: RecurrenceType = Field(
        default=RecurrenceType.DAILY,
        sa_column=Column(SAEnum(RecurrenceType, native_enum=False, length=10)),
    )
    recurrence_interval: int = 1
    recurrence_start_date: Optional[date] = None
    recurrence_end_date: Optional[date] = None

    # App configuration and state
    config: dict[str, Any] = Field(default_factory=dict, sa_column=Column(JSON))
    empty_last_render: bool = False
    render_messages: list[str] = Field(default_factory=list, sa_column=Column(JSON))
    autopin: bool = False
    recurrence_pattern: Optional[dict[str, Any]] = Field(
        default=None, sa_column=Column(JSON)
    )

    # Foreign key
    device_id: str = Field(foreign_key="devices.id", index=True)

    # Relationships
    device: "DeviceDB" = Relationship(back_populates="apps")
