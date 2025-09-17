from typing import Dict, List, Optional

from pydantic import BaseModel, Field


class Location(BaseModel):
    name: Optional[str] = None
    timezone: Optional[str] = None
    lat: float
    lng: float


class App(BaseModel):
    name: str
    iname: str
    enabled: bool
    last_render: Optional[int] = None
    path: Optional[str] = None
    uinterval: Optional[int] = None
    display_time: Optional[int] = None
    notes: Optional[str] = None
    id: Optional[str] = None
    config: Dict = Field(default_factory=dict)
    order: Optional[int] = None
    pushed: Optional[bool] = False
    empty_last_render: Optional[bool] = False
    start_time: Optional[str] = None
    end_time: Optional[str] = None
    days: List[str] = Field(default_factory=list)


class Device(BaseModel):
    id: str
    name: Optional[str] = None
    type: Optional[str] = None
    api_key: Optional[str] = None
    img_url: Optional[str] = None
    ws_url: Optional[str] = None
    notes: Optional[str] = None
    brightness: Optional[int] = None
    night_mode_enabled: Optional[bool] = False
    night_mode_app: Optional[str] = None
    night_start: Optional[int] = None
    night_end: Optional[int] = None
    night_brightness: Optional[int] = None
    default_interval: Optional[int] = None
    timezone: Optional[str] = None
    location: Optional[Location] = None
    apps: Dict[str, App] = Field(default_factory=dict)
    last_app_index: Optional[int] = None


class User(BaseModel):
    username: str
    password: str
    email: Optional[str] = None
    devices: Dict[str, Device] = Field(default_factory=dict)
    api_key: Optional[str] = None
    theme_preference: Optional[str] = "system"
