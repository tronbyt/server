import ctypes
import datetime as dt
import json
import os
import time
from pathlib import Path
from threading import Lock
from typing import Any, Callable, Dict, List, Optional, Tuple

from babel.dates import format_timedelta
from dotenv import find_dotenv, load_dotenv
from flask import Flask, current_app, g, request
from flask_babel import Babel, _
from flask_sock import Sock
from werkzeug.serving import is_running_from_reloader

from tronbyt_server import db, system_apps

babel = Babel()
sock = Sock()
pixlet_render_app: Optional[
    Callable[[bytes, bytes, int, int, int, int, int, int, int, Optional[bytes]], Any]
] = None
pixlet_get_schema: Optional[Callable[[bytes], Any]] = None
pixlet_call_handler: Optional[
    Callable[[ctypes.c_char_p, ctypes.c_char_p, ctypes.c_char_p], Any]
] = None
pixlet_init_cache: Optional[Callable[[], None]] = None
pixlet_init_redis_cache: Optional[Callable[[bytes], None]] = None
pixlet_free_bytes: Optional[Callable[[Any], None]] = None


def load_pixlet_library() -> None:
    libpixlet_path = Path(os.getenv("LIBPIXLET_PATH", "/usr/lib/libpixlet.so"))
    current_app.logger.info(f"Loading {libpixlet_path}")
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


def initialize_pixlet_library() -> None:
    global _pixlet_initialized
    with _pixlet_lock:
        if _pixlet_initialized:
            return

        load_pixlet_library()

        redis_url = os.getenv("REDIS_URL")
        if redis_url and pixlet_init_redis_cache:
            current_app.logger.info(f"Using Redis cache at {redis_url}")
            pixlet_init_redis_cache(redis_url.encode("utf-8"))
        elif pixlet_init_cache:
            pixlet_init_cache()
        _pixlet_initialized = True


def c_char_p_to_string(c_pointer: ctypes.c_char_p) -> Optional[str]:
    if not c_pointer:
        return None
    data = ctypes.string_at(c_pointer)  # Extract the NUL-terminated C-String
    result = data.decode("utf-8")  # Decode the C-String to Python string
    if pixlet_free_bytes:
        pixlet_free_bytes(c_pointer)  # Free the original C pointer
    return result


def render_app(
    path: Path,
    config: Dict[str, Any],
    width: int,
    height: int,
    magnify: int,
    maxDuration: int,
    timeout: int,
    image_format: int,
) -> Tuple[Optional[bytes], List[str]]:
    initialize_pixlet_library()
    if not pixlet_render_app:
        current_app.logger.debug("failed to init pixlet_library")
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
        current_app.logger.error(f"Error while rendering {path}: {error}")
    if ret.length >= 0:
        data = ctypes.cast(
            ret.data, ctypes.POINTER(ctypes.c_byte * ret.length)
        ).contents
        buf = bytes(data)
        if pixlet_free_bytes and ret.data:
            pixlet_free_bytes(ret.data)
        if messagesJSON:
            try:
                messages = json.loads(messagesJSON)
            except Exception as e:
                current_app.logger.error(f"Error: {e}")
                messages = []
        return buf, messages
    return None, []


def get_schema(path: Path) -> Optional[str]:
    initialize_pixlet_library()
    if not pixlet_get_schema:
        return None
    ret = pixlet_get_schema(str(path).encode("utf-8"))
    schema = c_char_p_to_string(ret.data)
    if ret.status != 0:
        return None
    return schema


def call_handler(path: Path, handler: str, parameter: str) -> Optional[str]:
    initialize_pixlet_library()
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


def get_locale() -> Optional[str]:
    return request.accept_languages.best_match(current_app.config["LANGUAGES"])


def create_app(test_config: Optional[Dict[str, Any]] = None) -> Flask:
    if dotenv_path := find_dotenv(usecwd=True):
        print(f"Loading environment variables from {dotenv_path}")
        load_dotenv(dotenv_path)

    # create and configure the app
    app = Flask(__name__, instance_relative_config=True)
    if test_config is None:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_HOSTNAME=os.getenv("SERVER_HOSTNAME", "localhost"),
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="users",
            DATA_DIR=os.getenv("DATA_DIR", "data"),
            PRODUCTION=os.getenv("PRODUCTION", "1"),
            DB_FILE="users/usersdb.sqlite",
            LANGUAGES=["en", "de"],
            MAX_USERS=int(os.getenv("MAX_USERS", "100")),
        )
        if app.config.get("PRODUCTION") == "1":
            if app.config["SERVER_PROTOCOL"] == "https":
                app.config["SESSION_COOKIE_SECURE"] = True
            app.config["SESSION_COOKIE_SAMESITE"] = "Lax"
            app.logger.setLevel(os.getenv("LOG_LEVEL", "WARNING"))
        else:
            app.logger.setLevel(os.getenv("LOG_LEVEL", "INFO"))
    else:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            DB_FILE="tests/users/testdb.sqlite",
            LANGUAGES=["en"],
            SERVER_HOSTNAME="localhost",
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="tests/users",
            DATA_DIR=os.getenv("DATA_DIR", "data"),
            PRODUCTION="0",
            MAX_USERS=int(os.getenv("MAX_USERS", "100")),
            TESTING=True,
        )
    babel.init_app(app, locale_selector=get_locale)

    instance_path = Path(app.instance_path)
    try:
        instance_path.mkdir(parents=True, exist_ok=True)
    except OSError:
        pass

    # Initialize the database within the application context
    with app.app_context():
        db.init_db()

        # The reloader will run this code twice, once in the main process and once in the child process.
        # This is a workaround to avoid running the update functions twice.
        if not is_running_from_reloader():
            # Update firmware before updating apps
            system_apps.update_firmware_binaries(db.get_data_dir())
            system_apps.update_system_repo(db.get_data_dir())

    from . import auth

    app.register_blueprint(auth.bp)

    from . import api

    app.register_blueprint(api.bp)

    from . import manager

    app.register_blueprint(manager.bp)
    app.add_url_rule("/", endpoint="index")

    sock.init_app(app)

    @app.template_filter("timeago")
    def timeago(seconds: int) -> str:
        if seconds == 0:
            return str(_("Never"))
        return format_timedelta(
            dt.timedelta(seconds=seconds - int(time.time())),
            granularity="second",
            add_direction=True,
            locale=get_locale(),
        )

    @app.teardown_appcontext
    def close_connection(exception: Any) -> None:
        db = getattr(g, "_database", None)
        if db is not None:
            db.close()

    return app
