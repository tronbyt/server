from typing import Dict, Required, TypedDict, Literal

from tronbyt_server.models.device import Device


class User(TypedDict, total=False):
    username: Required[str]
    password: Required[str]
    email: str
    devices: Dict[str, Device]
    api_key: str
    theme_preference: Literal["light", "dark", "system"]
