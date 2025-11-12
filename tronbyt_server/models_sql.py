from datetime import datetime, time, date
from typing import List, Optional, Any

from sqlmodel import Field, Relationship, SQLModel, JSON, Column


class User(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    username: str = Field(unique=True, index=True)
    password: str
    email: str = ""
    api_key: str = ""
    theme_preference: str = "system"
    system_repo_url: str = ""
    app_repo_url: str = ""

    devices: List["Device"] = Relationship(back_populates="user")


class Device(SQLModel, table=True):
    id: str = Field(primary_key=True)  # This is the device's unique hardware ID
    name: str = ""
    type: str = "tidbyt_gen1"
    api_key: str = ""
    img_url: str = ""
    ws_url: str = ""
    notes: str = ""
    brightness: int = 100
    night_mode_enabled: bool = False
    night_mode_app: str = ""
    night_start: Optional[str] = None
    night_end: Optional[str] = None
    night_brightness: int = 0
    dim_time: Optional[str] = None
    dim_brightness: Optional[int] = None
    default_interval: int = 15
    timezone: Optional[str] = None
    location: Optional[dict[str, Any]] = Field(default=None, sa_column=Column(JSON))
    last_app_index: int = 0
    pinned_app: Optional[str] = None
    interstitial_enabled: bool = False
    interstitial_app: Optional[str] = None
    last_seen: Optional[datetime] = None
    info: Optional[dict[str, Any]] = Field(default=None, sa_column=Column(JSON))

    user_id: Optional[int] = Field(default=None, foreign_key="user.id")
    user: User = Relationship(back_populates="devices")

    apps: List["App"] = Relationship(back_populates="device")


class App(SQLModel, table=True):
    id: Optional[int] = Field(default=None, primary_key=True)
    iname: str  # instance name
    name: str
    uinterval: int = 0
    display_time: int = 0
    notes: str = ""
    enabled: bool = True
    pushed: bool = False
    order: int = 0
    last_render: int = 0
    path: Optional[str] = None
    start_time: Optional[time] = None
    end_time: Optional[time] = None
    days: Optional[List[str]] = Field(default=None, sa_column=Column(JSON))
    use_custom_recurrence: bool = False
    recurrence_type: str = "daily"
    recurrence_interval: int = 1
    recurrence_pattern: Optional[dict[str, Any]] = Field(
        default=None, sa_column=Column(JSON)
    )
    recurrence_start_date: Optional[date] = None
    recurrence_end_date: Optional[date] = None
    config: Optional[dict[str, Any]] = Field(default=None, sa_column=Column(JSON))
    empty_last_render: bool = False
    render_messages: Optional[List[str]] = Field(default=None, sa_column=Column(JSON))
    autopin: bool = False

    device_id: str = Field(foreign_key="device.id")
    device: Device = Relationship(back_populates="apps")
