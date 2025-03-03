from typing import Dict, TypedDict

from tronbyt_server.models.app import App


class Device(TypedDict, total=False):
    id: str
    name: str
    api_key: str
    img_url: str
    notes: str
    brightness: int
    night_mode_enabled: bool
    night_mode_app: str
    night_start: int
    night_end: int
    night_brightness: int
    default_interval: int
    timezone: str
    apps: Dict[str, App]
    firmware_file_path: str
    last_app_index: int
