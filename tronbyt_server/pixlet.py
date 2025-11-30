"""Wrapper around the Pixlet C library."""

import ctypes
import json
import logging
import platform
from pathlib import Path
from threading import Lock
from typing import Any, Callable

from tronbyt_server.config import get_settings

logger = logging.getLogger(__name__)

# Corresponds to libpixlet API, incremented for breaking changes.
# libpixlet does not guarantee forwards or backwards compatibility yet.
EXPECTED_LIBPIXLET_API_VERSION = 1


pixlet_render_app: (
    Callable[
        [
            bytes,  # path
            bytes,  # config
            int,  # width
            int,  # height
            int,  # maxDuration
            int,  # timeout
            int,  # imageFormat
            int,  # silenceOutput
            bool,  # output2x
            bytes | None,  # filters
            bytes | None,  # tz
            bytes | None,  # locale
        ],
        Any,
    ]
    | None
) = None
pixlet_get_schema: Callable[[bytes, int, int, bool], Any] | None = (
    None  # path, width, height, output2x
)
pixlet_call_handler: (
    Callable[
        [
            ctypes.c_char_p,  # path
            ctypes.c_char_p,  # config
            int,  # width
            int,  # height
            bool,  # output2x
            ctypes.c_char_p,  # handlerName
            ctypes.c_char_p,  # parameter
        ],
        Any,
    ]
    | None
) = None
pixlet_init_cache: Callable[[], None] | None = None
pixlet_init_redis_cache: Callable[[bytes], None] | None = None
pixlet_free_bytes: Callable[[Any], None] | None = None


# Constants for default libpixlet paths
_LIBPIXLET_PATH_LINUX = Path("/usr/lib/libpixlet.so")
_LIBPIXLET_PATH_MACOS_ARM = Path("/opt/homebrew/lib/libpixlet.dylib")
_LIBPIXLET_PATH_MACOS_INTEL = Path("/usr/local/lib/libpixlet.dylib")


def load_pixlet_library() -> None:
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

    try:
        api_version = ctypes.c_int.in_dll(pixlet_library, "libpixletAPIVersion").value
    except ValueError:
        api_version = 0

    logger.info(f"Libpixlet API version: {api_version}")
    if api_version != EXPECTED_LIBPIXLET_API_VERSION:
        raise RuntimeError(
            f"FATAL: libpixlet API version mismatch. Expected {EXPECTED_LIBPIXLET_API_VERSION}, found {api_version}"
        )

    global pixlet_init_redis_cache
    pixlet_init_redis_cache = pixlet_library.init_redis_cache
    pixlet_init_redis_cache.argtypes = [ctypes.c_char_p]

    global pixlet_init_cache
    pixlet_init_cache = pixlet_library.init_cache

    global pixlet_render_app
    pixlet_render_app = pixlet_library.render_app
    pixlet_render_app.argtypes = [
        ctypes.c_char_p,  # path
        ctypes.c_char_p,  # config
        ctypes.c_int,  # width
        ctypes.c_int,  # height
        ctypes.c_int,  # maxDuration
        ctypes.c_int,  # timeout
        ctypes.c_int,  # imageFormat
        ctypes.c_int,  # silenceOutput
        ctypes.c_bool,  # output2x
        ctypes.c_char_p,  # filters
        ctypes.c_char_p,  # tz
        ctypes.c_char_p,  # locale
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

    class CallHandlerReturn(ctypes.Structure):
        _fields_ = [
            ("data", ctypes.c_void_p),
            ("status", ctypes.c_int),
            ("error", ctypes.c_void_p),
        ]

    pixlet_render_app.restype = RenderAppReturn

    global pixlet_get_schema
    pixlet_get_schema = pixlet_library.get_schema
    pixlet_get_schema.argtypes = [
        ctypes.c_char_p,  # path
        ctypes.c_int,  # width
        ctypes.c_int,  # height
        ctypes.c_bool,  # output2x
    ]
    pixlet_get_schema.restype = StringReturn

    global pixlet_call_handler
    pixlet_call_handler = pixlet_library.call_handler
    pixlet_call_handler.argtypes = [
        ctypes.c_char_p,  # path
        ctypes.c_char_p,  # config
        ctypes.c_int,  # width
        ctypes.c_int,  # height
        ctypes.c_bool,  # output2x
        ctypes.c_char_p,  # handlerName
        ctypes.c_char_p,  # parameter
    ]
    pixlet_call_handler.restype = CallHandlerReturn

    global pixlet_free_bytes
    pixlet_free_bytes = pixlet_library.free_bytes
    pixlet_free_bytes.argtypes = [ctypes.c_void_p]


_pixlet_initialized = False
_pixlet_lock = Lock()


def initialize_pixlet_library() -> None:
    global _pixlet_initialized
    if _pixlet_initialized:
        return
    with _pixlet_lock:
        if _pixlet_initialized:
            return

        load_pixlet_library()

        settings = get_settings()
        redis_url = settings.REDIS_URL
        if redis_url and pixlet_init_redis_cache:
            logger.info(f"Using Redis cache at {redis_url}")
            pixlet_init_redis_cache(redis_url.encode("utf-8"))
        elif pixlet_init_cache:
            pixlet_init_cache()
        _pixlet_initialized = True


def c_char_p_to_string(c_pointer: ctypes.c_char_p | None) -> str | None:
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
    maxDuration: int,
    timeout: int,
    image_format: int,
    supports2x: bool = False,
    filters: dict[str, Any] | None = None,
    tz: str | None = None,
    locale: str | None = None,
) -> tuple[bytes | None, list[str]]:
    initialize_pixlet_library()
    if not pixlet_render_app:
        logger.debug("failed to init pixlet_library")
        return None, []
    filters_json = json.dumps(filters).encode("utf-8") if filters else None
    tz_bytes = tz.encode("utf-8") if tz else None
    locale_bytes = locale.encode("utf-8") if locale else None
    ret = pixlet_render_app(
        str(path).encode("utf-8"),
        json.dumps(config).encode("utf-8"),
        width,
        height,
        maxDuration,
        timeout,
        image_format,
        1,
        supports2x,
        filters_json,
        tz_bytes,
        locale_bytes,
    )
    error = c_char_p_to_string(ret.error)
    messagesJSON = c_char_p_to_string(ret.messages)
    if error:
        logger.error(f"Error while rendering {path}: {error}")
    if ret.length >= 0:
        buf = ctypes.string_at(ret.data, ret.length)
        if pixlet_free_bytes and ret.data:
            pixlet_free_bytes(ret.data)
        messages: list[str] = []
        if messagesJSON:
            try:
                messages = json.loads(messagesJSON)
            except Exception as e:
                logger.error(f"Error: {e}")
        return buf, messages

    match ret.length:
        case -1:
            logger.error(f"Invalid config for {path}")
        case -2:
            logger.error(f"Render failure for {path}")
        case -3:
            logger.error(f"Invalid filters for {path}")
        case -4:
            logger.error(f"Handler failure for {path}")
        case -5:
            logger.error(f"Invalid path for {path}")
        case -6:
            logger.error(f"Star suffix error for {path}")
        case -7:
            logger.error(f"Unknown applet for {path}")
        case -8:
            logger.error(f"Schema failure for {path}")
        case -9:
            logger.error(f"Invalid timezone for {path}")
        case -10:
            logger.error(f"Invalid locale for {path}")
        case _:
            logger.error(f"Unknown error for {path}: {ret.length}")

    return None, []


def get_schema(path: Path, width: int, height: int, supports2x: bool) -> str | None:
    initialize_pixlet_library()
    if not pixlet_get_schema:
        return None
    ret = pixlet_get_schema(str(path).encode("utf-8"), width, height, supports2x)
    schema = c_char_p_to_string(ret.data)
    if ret.status != 0:
        return None
    return schema


def call_handler(
    path: Path,
    config: dict[str, Any],
    handler: str,
    parameter: str,
    width: int,
    height: int,
    supports2x: bool,
) -> str | None:
    initialize_pixlet_library()
    if not pixlet_call_handler:
        return None
    ret = pixlet_call_handler(
        ctypes.c_char_p(str(path).encode("utf-8")),
        ctypes.c_char_p(json.dumps(config).encode("utf-8")),
        width,
        height,
        supports2x,
        ctypes.c_char_p(handler.encode("utf-8")),
        ctypes.c_char_p(parameter.encode("utf-8")),
    )
    res = c_char_p_to_string(ret.data)
    error = c_char_p_to_string(ret.error)
    if error:
        logger.error(f"Error while calling handler {handler} for {path}: {error}")
    if ret.status != 0:
        return None
    return res
