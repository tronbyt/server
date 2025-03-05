from typing import Dict, TypedDict

from tronbyt_server.models.device import Device


class User(TypedDict, total=False):
    username: str
    password: str
    email: str
    devices: Dict[str, Device]
