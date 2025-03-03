import ctypes
import datetime as dt
import json
import os
import time
from typing import Any, Callable, Dict, Optional

from babel.dates import format_timedelta
from dotenv import load_dotenv
from flask import Flask, current_app, g, request
from flask_babel import Babel, _

from tronbyt_server import db

babel = Babel()
pixlet_render_app: Optional[
    Callable[[bytes, bytes, int, int, int, int, int, int, int], Any]
] = None
free_bytes: Optional[Callable[[Any], None]] = None


def initialize_pixlet_library(app: Flask) -> None:
    libpixlet_path = os.getenv("LIBPIXLET_PATH", "/usr/lib/libpixlet.so")
    app.logger.info(f"Loading {libpixlet_path}")
    try:
        pixlet_library = ctypes.cdll.LoadLibrary(libpixlet_path)
    except OSError as e:
        raise RuntimeError(f"Failed to load {libpixlet_path}: {e}")

    redis_url = os.getenv("REDIS_URL")
    if redis_url:
        app.logger.info(f"Using Redis cache at {redis_url}")
        init_redis_cache = pixlet_library.init_redis_cache
        init_redis_cache.argtypes = [ctypes.c_char_p]
        init_redis_cache(redis_url.encode("utf-8"))
    else:
        init_cache = pixlet_library.init_cache
        init_cache()

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
    ]

    class RenderAppReturn(ctypes.Structure):
        _fields_ = [
            ("data", ctypes.POINTER(ctypes.c_ubyte)),
            ("length", ctypes.c_int),
        ]

    pixlet_render_app.restype = RenderAppReturn

    global free_bytes

    free_bytes = pixlet_library.free_bytes
    free_bytes.argtypes = [ctypes.POINTER(ctypes.c_ubyte)]


def render_app(
    name: str,
    config: Dict[str, Any],
    width: int,
    height: int,
    magnify: int,
    maxDuration: int,
    timeout: int,
    render_gif: bool,
    silence_output: bool,
) -> Optional[bytes]:
    if pixlet_render_app is None:
        return None
    ret = pixlet_render_app(
        name.encode("utf-8"),
        json.dumps(config).encode("utf-8"),
        width,
        height,
        magnify,
        maxDuration,
        timeout,
        1 if render_gif else 0,
        1 if silence_output else 0,
    )
    if ret.length >= 0:
        data = ctypes.cast(
            ret.data, ctypes.POINTER(ctypes.c_uint32 * ret.length)
        ).contents
        buf = bytes(data)
        if free_bytes:
            free_bytes(ret.data)
        return buf
    return None


def get_locale() -> Optional[str]:
    return request.accept_languages.best_match(current_app.config["LANGUAGES"])


def create_app(test_config: Optional[Dict[str, Any]] = None) -> Flask:
    load_dotenv()
    # create and configure the app
    app = Flask(__name__, instance_relative_config=True)
    if test_config is None:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_HOSTNAME=os.getenv("SERVER_HOSTNAME", "localhost"),
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            PIXLET_RENDER_PORT1=os.getenv("PIXLET_RENDER_PORT1", "5100"),
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="users",
            PRODUCTION=os.getenv("PRODUCTION", "1"),
            DB_FILE="users/usersdb.sqlite",
            LANGUAGES=["en", "de"],
        )
        if app.config.get("PRODUCTION") == "1":
            if app.config["SERVER_PROTOCOL"] == "https":
                app.config["SESSION_COOKIE_SECURE"] = True
            app.config["SESSION_COOKIE_SAMESITE"] = "Lax"
    else:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            PIXLET_RENDER_PORT1=os.getenv("PIXLET_RENDER_PORT1", "5100"),
            DB_FILE="users/testdb.sqlite",
            LANGUAGES=["en"],
            SERVER_HOSTNAME="localhost",
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="tests/users",
            PRODUCTION="0",
            TESTING=True,
        )
    babel.init_app(app, locale_selector=get_locale)

    try:
        os.makedirs(app.instance_path)
    except OSError:
        pass

    if test_config is None:
        initialize_pixlet_library(app)

    # Initialize the database within the application context
    with app.app_context():
        db.init_db()

    from . import auth

    app.register_blueprint(auth.bp)

    from . import api

    app.register_blueprint(api.bp)
    # app.add_url_rule("/api", endpoint="api")

    from . import manager

    app.register_blueprint(manager.bp)
    app.add_url_rule("/", endpoint="index")

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
