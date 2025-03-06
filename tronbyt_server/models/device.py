import re
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


def validate_device_id(device_id: str) -> bool:
    """
    Validate the device ID to ensure it meets the required format.
    A valid device ID should be a string of exactly 8 hexadecimal characters.

    :param device_id: The device ID to validate.
    :return: True if the device ID is valid, False otherwise.
    """
    if not device_id:
        return False
    return bool(re.match(r"^[a-fA-F0-9]{8}$", device_id))
