import ctypes
import logging
import os
from contextlib import asynccontextmanager
from pathlib import Path

from dotenv import find_dotenv, load_dotenv
from fastapi import FastAPI, WebSocket
from fastapi.staticfiles import StaticFiles
from starlette.middleware.sessions import SessionMiddleware
from starlette.requests import Request

from tronbyt_server.templating import templates

logger = logging.getLogger("uvicorn.error")

pixlet_render_app = None
pixlet_get_schema = None
pixlet_call_handler = None
pixlet_init_cache = None
pixlet_init_redis_cache = None
pixlet_free_bytes = None


def load_pixlet_library() -> None:
    libpixlet_path = Path(os.getenv("LIBPIXLET_PATH", "/usr/lib/libpixlet.so"))
    logger.info(f"Loading {libpixlet_path}")
    try:
        pixlet_library = ctypes.cdll.LoadLibrary(str(libpixlet_path))
    except (OSError, AttributeError) as e:
        logger.warning(f"Failed to load {libpixlet_path}: {e}")
        return

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


@asynccontextmanager
async def lifespan(app: FastAPI):
    # Load the environment variables
    if dotenv_path := find_dotenv(usecwd=True):
        print(f"Loading environment variables from {dotenv_path}")
        load_dotenv(dotenv_path)

    # Load the pixlet library
    load_pixlet_library()

    # Initialize the cache
    redis_url = os.getenv("REDIS_URL")
    if redis_url and pixlet_init_redis_cache:
        logger.info(f"Using Redis cache at {redis_url}")
        pixlet_init_redis_cache(redis_url.encode("utf-8"))
    elif pixlet_init_cache:
        pixlet_init_cache()

    db.init_db(logger)
    try:
        system_apps.update_firmware_binaries(logger, db.get_data_dir())
    except Exception as e:
        logger.error(f"Failed to update firmware during startup: {e}")
    system_apps.update_system_repo(logger, db.get_data_dir())
    yield


app = FastAPI(lifespan=lifespan)
app.mount("/static", StaticFiles(directory="tronbyt_server/static"), name="static")
app.add_middleware(SessionMiddleware, secret_key="super-secret-key-that-you-should-change")

from tronbyt_server import system_apps_fastapi as system_apps
from tronbyt_server import db_fastapi as db
import datetime as dt
import time
from babel.dates import format_timedelta
from tronbyt_server.manager_fastapi import router as manager_router
from tronbyt_server.api_fastapi import router as api_router
from tronbyt_server.auth_fastapi import router as auth_router

app.include_router(manager_router)
app.include_router(api_router)
app.include_router(auth_router)

def flash(request: Request, message: str, category: str = "primary") -> None:
    if "_messages" not in request.session:
        request.session["_messages"] = []
    request.session["_messages"].append({"message": message, "category": category})

def get_flashed_messages(request: Request):
    return request.session.pop("_messages") if "_messages" in request.session else []

def timeago(seconds: int) -> str:
    if seconds == 0:
        return "Never"
    return format_timedelta(
        dt.timedelta(seconds=seconds - int(time.time())),
        granularity="second",
        add_direction=True,
        locale="en",
    )

from tronbyt_server.connection_manager import manager

templates.env.filters["timeago"] = timeago
templates.env.globals["get_flashed_messages"] = get_flashed_messages
