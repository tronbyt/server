import ctypes
from pathlib import Path
from typing import Optional

from tronbyt_server.main import pixlet_free_bytes


def c_char_p_to_string(c_pointer: ctypes.c_char_p) -> Optional[str]:
    if not c_pointer:
        return None
    data = ctypes.string_at(c_pointer)  # Extract the NUL-terminated C-String
    result = data.decode("utf-8")  # Decode the C-String to Python string
    if pixlet_free_bytes:
        pixlet_free_bytes(c_pointer)  # Free the original C pointer
    return result


def get_schema(path: Path) -> Optional[str]:
    from tronbyt_server.main import pixlet_get_schema

    if not pixlet_get_schema:
        return None
    ret = pixlet_get_schema(str(path).encode("utf-8"))
    schema = c_char_p_to_string(ret.data)
    if ret.status != 0:
        return None
    return schema


def call_handler(path: Path, handler: str, parameter: str) -> Optional[str]:
    from tronbyt_server.main import pixlet_call_handler

    if not pixlet_call_handler:
        return None
    ret = pixlet_call_handler(
        ctypes.c_char_p(str(path).encode("utf-8")),
        ctypes.c_char_p(handler.encode("utf-8")),
        ctypes.c_char_p(parameter.encode("utf-8")),
    )
    res = c_char_p_to_string(ret.data)
    if ret.status != 0:
        return None
    return res
