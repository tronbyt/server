"""Data models for Tronbyt Server applications."""

from typing import Any, Dict, List, Optional, Required, TypedDict, Union


class RecurrencePattern(TypedDict, total=False):
    """Recurrence pattern for monthly/yearly schedules."""

    day_of_month: int  # 1-31 for specific day of month
    day_of_week: str  # "first_monday", "second_tuesday", "last_friday", etc.
    weekdays: List[str]  # For weekly patterns: ["monday", "wednesday"]


class App(TypedDict, total=False):
    id: str
    iname: Required[str]
    name: Required[str]
    uinterval: int
    display_time: int
    notes: str
    enabled: bool
    pushed: bool
    order: int
    last_render: int
    path: str
    start_time: str
    end_time: str
    days: List[str]
    # Custom recurrence system (opt-in)
    use_custom_recurrence: bool  # Flag to enable custom recurrence instead of legacy
    recurrence_type: str  # "daily", "weekly", "monthly", "yearly"
    recurrence_interval: int  # Every X weeks/months/years (default: 1)
    recurrence_pattern: Union[
        List[str], RecurrencePattern
    ]  # Pattern within recurrence type
    recurrence_start_date: str  # ISO date string for calculating cycles (YYYY-MM-DD)
    recurrence_end_date: Optional[str]  # Optional end date for recurrence (YYYY-MM-DD)
    config: Dict[str, Any]
    empty_last_render: bool
    render_messages: List[str]  # Changed from str to List[str]


class AppMetadata(TypedDict, total=False):
    id: str
    name: str
    summary: str
    desc: str
    author: str
    path: str
    fileName: Optional[str]
    packageName: Optional[str]
    preview: Optional[str]
    supports2x: bool
    recommended_interval: int
