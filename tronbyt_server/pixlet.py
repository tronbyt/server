"""Wrapper around the Pixlet C library."""

import ctypes
import json
import platform
from logging import Logger
from pathlib import Path
from threading import Lock
from typing import Any, Callable

from tronbyt_server.config import get_settings

pixlet_render_app: (
    Callable[[bytes, bytes, int, int, int, int, int, int, int, bytes | None], Any]
    | None
) = None
pixlet_get_schema: Callable[[bytes], Any] | None = None
pixlet_call_handler: (
    Callable[[ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p], Any] | None
) = None
pixlet_init_cache: Callable[[], None] | None = None
pixlet_init_redis_cache: Callable[[bytes], None] | None = None
pixlet_free_bytes: Callable[[Any], None] | None = None


# Constants for default libpixlet paths
_LIBPIXLET_PATH_LINUX = Path("/usr/lib/libpixlet.so")
_LIBPIXLET_PATH_MACOS_ARM = Path("/opt/homebrew/lib/libpixlet.dylib")
_LIBPIXLET_PATH_MACOS_INTEL = Path("/usr/local/lib/libpixlet.dylib")


def load_pixlet_library(logger: Logger) -> None:
    libpixlet_path_str = get_settings().LIBPIXLET_PATH
    if libpixlet_path_str:
        libpixlet_path = Path(libpixlet_path_str)
    else:
        system = platform.system()
        if system == "Darwin":
            # Start with the Apple Silicon path
            libpixlet_path = _LIBPIXLET_PATH_MACOS_ARM
            if not libpixlet_path.exists():
                # Fallback to the Intel path if the ARM path doesn't exist
                libpixlet_path = _LIBPIXLET_PATH_MACOS_INTEL
        else:  # Linux and others
            libpixlet_path = _LIBPIXLET_PATH_LINUX

    logger.info(f"Loading {libpixlet_path}")
    try:
        pixlet_library = ctypes.cdll.LoadLibrary(str(libpixlet_path))
    except OSError as e:
        raise RuntimeError(f"Failed to load {libpixlet_path}: {e}")

    global pixlet_init_redis_cache
    pixlet_init_redis_cache = pixlet_library.init_redis_cache
    pixlet_init_redis_cache.argtypes = [ctypes.c_char_p]

    global pixlet_init_cache
    pixlet_init_cache = pixlet_library.init_cache

    global pixlet_render_app
    pixlet_render_app = pixlet_library.render_app
    pixlet_render_app.argtypes = [
        ctypes.c_char_p,
        ctypes.c_char_p,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_int,
        ctypes.c_char_p,
    ]

    # Use c_void_p for the return type to avoid ctype's automatic copying into bytes() objects.
    # We need the exact pointer value so that we can free it later using pixlet_free_bytes.
    class RenderAppReturn(ctypes.Structure):
        _fields_ = [
            ("data", ctypes.c_void_p),
            ("length", ctypes.c_int),
            ("messages", ctypes.c_void_p),
            ("error", ctypes.c_void_p),
        ]

    class StringReturn(ctypes.Structure):
        _fields_ = [
            ("data", ctypes.c_void_p),
            ("status", ctypes.c_int),
        ]

    pixlet_render_app.restype = RenderAppReturn

    global pixlet_get_schema
    pixlet_get_schema = pixlet_library.get_schema
    pixlet_get_schema.argtypes = [ctypes.c_char_p]
    pixlet_get_schema.restype = StringReturn

    global pixlet_call_handler
    pixlet_call_handler = pixlet_library.call_handler
    pixlet_call_handler.argtypes = [
        ctypes.c_char_p,
        ctypes.c_char_p,
        ctypes.c_char_p,
    ]
    pixlet_call_handler.restype = StringReturn

    global pixlet_free_bytes
    pixlet_free_bytes = pixlet_library.free_bytes
    pixlet_free_bytes.argtypes = [ctypes.c_void_p]


_pixlet_initialized = False
_pixlet_lock = Lock()


def initialize_pixlet_library(logger: Logger) -> None:
    global _pixlet_initialized
    with _pixlet_lock:
        if _pixlet_initialized:
            return

        load_pixlet_library(logger)

        settings = get_settings()
        redis_url = settings.REDIS_URL
        if redis_url and pixlet_init_redis_cache:
            logger.info(f"Using Redis cache at {redis_url}")
            pixlet_init_redis_cache(redis_url.encode("utf-8"))
        elif pixlet_init_cache:
            pixlet_init_cache()
        _pixlet_initialized = True


def c_char_p_to_string(c_pointer: ctypes.c_char_p) -> str | None:
    if not c_pointer:
        return None
    data = ctypes.string_at(c_pointer)  # Extract the NUL-terminated C-String
    result = data.decode("utf-8")  # Decode the C-String to Python string
    if pixlet_free_bytes:
        pixlet_free_bytes(c_pointer)  # Free the original C pointer
    return result


def render_app(
    path: Path,
    config: dict[str, Any],
    width: int,
    height: int,
    magnify: int,
    maxDuration: int,
    timeout: int,
    image_format: int,
    logger: Logger,
) -> tuple[bytes | None, list[str]]:
    initialize_pixlet_library(logger)
    if not pixlet_render_app:
        logger.debug("failed to init pixlet_library")
        return None, []
    ret = pixlet_render_app(
        str(path).encode("utf-8"),
        json.dumps(config).encode("utf-8"),
        width,
        height,
        magnify,
        maxDuration,
        timeout,
        image_format,
        1,
        None,
    )
    error = c_char_p_to_string(ret.error)
    messagesJSON = c_char_p_to_string(ret.messages)
    if error:
        logger.error(f"Error while rendering {path}: {error}")
    if ret.length >= 0:
        data = ctypes.cast(
            ret.data, ctypes.POINTER(ctypes.c_byte * ret.length)
        ).contents
        buf = bytes(data)
        if pixlet_free_bytes and ret.data:
            pixlet_free_bytes(ret.data)
        messages: list[str] = []
        if messagesJSON:
            try:
                messages = json.loads(messagesJSON)
            except Exception as e:
                logger.error(f"Error: {e}")
        return buf, messages
    return None, []


def get_schema(path: Path, logger: Logger) -> str | None:
    initialize_pixlet_library(logger)
    if not pixlet_get_schema:
        return None
    ret = pixlet_get_schema(str(path).encode("utf-8"))
    schema = c_char_p_to_string(ret.data)
    if ret.status != 0:
        return None
    return schema


def call_handler(
    path: Path, handler: str, parameter: str, logger: Logger
) -> str | None:
    initialize_pixlet_library(logger)
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
