"""Data models for Tronbyt Server applications."""

from enum import Enum
from typing import Any
from pydantic import BaseModel, Field, BeforeValidator
from datetime import time, date
from typing import Annotated


class Weekday(str, Enum):
    """Weekday enumeration."""

    MONDAY = "monday"
    TUESDAY = "tuesday"
    WEDNESDAY = "wednesday"
    THURSDAY = "thursday"
    FRIDAY = "friday"
    SATURDAY = "saturday"
    SUNDAY = "sunday"


def parse_time(v: Any) -> Any:
    """Parse time from string."""
    if isinstance(v, str):
        try:
            return time.fromisoformat(v)
        except ValueError:
            return None
    return v


def parse_date_optional(v: Any) -> Any:
    """Parse date from string, allowing empty string as None."""
    if v == "":
        return None
    return v


class RecurrencePattern(BaseModel):
    """Recurrence pattern for monthly/yearly schedules."""

    day_of_month: int | None = None  # 1-31 for specific day of month
    day_of_week: str | None = (
        None  # "first_monday", "second_tuesday", "last_friday", etc.
    )
    weekdays: list[Weekday] | None = (
        None  # For weekly patterns: ["monday", "wednesday"]
    )


class RecurrenceType(str, Enum):
    """Recurrence type enumeration."""

    DAILY = "daily"
    WEEKLY = "weekly"
    MONTHLY = "monthly"
    YEARLY = "yearly"


class App(BaseModel):
    """Pydantic model for an app."""

    id: str | None = None
    iname: str
    name: str
    uinterval: int = 0  # Update interval for the app
    display_time: int = 0  # Display time for the app
    notes: str = ""  # User notes for the app
    enabled: bool = True
    pushed: bool = False
    order: int = 0  # Order in the app list
    last_render: int = 0
    path: str | None = None  # Path to the app file
    start_time: Annotated[time | None, BeforeValidator(parse_time)] = (
        None  # Optional start time (HH:MM)
    )
    end_time: Annotated[time | None, BeforeValidator(parse_time)] = (
        None  # Optional end time (HH:MM)
    )
    days: list[Weekday] = []
    # Custom recurrence system (opt-in)
    use_custom_recurrence: bool = (
        False  # Flag to enable custom recurrence instead of legacy
    )
    recurrence_type: RecurrenceType = Field(
        default=RecurrenceType.DAILY,
        description='"daily", "weekly", "monthly", "yearly"',
    )
    recurrence_interval: int = 1  # Every X weeks/months/years
    recurrence_pattern: RecurrencePattern = Field(default_factory=RecurrencePattern)
    recurrence_start_date: Annotated[
        date | None, BeforeValidator(parse_date_optional)
    ] = None  # ISO date string for calculating cycles (YYYY-MM-DD)
    recurrence_end_date: Annotated[
        date | None, BeforeValidator(parse_date_optional)
    ] = None  # Optional end date for recurrence (YYYY-MM-DD)
    config: dict[str, Any] = {}
    empty_last_render: bool = False
    render_messages: list[str] = []  # Changed from str to List[str]
    autopin: bool = False


class AppMetadata(BaseModel):
    """Pydantic model for app metadata."""

    id: str | None = None
    name: str
    summary: str = ""
    desc: str = ""
    author: str = ""
    path: str
    fileName: str | None = None
    packageName: str | None = None
    preview: str | None = None
    preview2x: str | None = None
    supports2x: bool = False
    recommended_interval: int = 0
    date: str = ""  # ISO date string for file modification date
    is_installed: bool = False  # Used to mark if app is installed on any device
    uinterval: int = 0  # Update interval for the app
    display_time: int = 0  # Display time for the app
    notes: str = ""  # User notes for the app
    order: int = 0  # Order in the app list
    broken: bool = False
    brokenReason: str | None = None
