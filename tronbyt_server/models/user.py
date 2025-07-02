from typing import Dict, Required, TypedDict

from tronbyt_server.models.device import Device


class User(TypedDict, total=False):
    username: Required[str]
    password: Required[str]
    email: str
    devices: Dict[str, Device]
    api_key: str
