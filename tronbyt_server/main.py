"""Main application file."""

import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import Response
from fastapi.staticfiles import StaticFiles
from fastapi_babel import Babel, BabelConfigs, BabelMiddleware
from starlette.middleware.sessions import SessionMiddleware

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import settings
from tronbyt_server.dependencies import (
    NotAuthenticatedException,
    auth_exception_handler,
    get_db,
)
from tronbyt_server.routers import api, auth, manager, websockets
from tronbyt_server.templates import templates


from typing import AsyncGenerator


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Run startup and shutdown events."""
    # Startup
    logging.basicConfig(level=settings.LOG_LEVEL)
    logger = logging.getLogger(__name__)
    db_connection = next(get_db())
    with db_connection:
        db.init_db(db_connection)
    try:
        firmware_utils.update_firmware_binaries_subprocess(db.get_data_dir(), logger)
    except Exception as e:
        logger.error(f"Failed to update firmware during startup: {e}")
    system_apps.update_system_repo(db.get_data_dir(), logger)
    yield
    # Shutdown
    from tronbyt_server.sync import get_sync_manager

    get_sync_manager(logger).shutdown()


app = FastAPI(lifespan=lifespan)

app.add_middleware(SessionMiddleware, secret_key=settings.SECRET_KEY)

# Babel configuration
babel_configs = BabelConfigs(
    ROOT_DIR=".",
    BABEL_DEFAULT_LOCALE="en",
    BABEL_TRANSLATION_DIRECTORY="tronbyt_server/translations",
)
app.add_middleware(
    BabelMiddleware, babel_configs=babel_configs, jinja2_templates=templates
)
Babel(configs=babel_configs)


@app.exception_handler(NotAuthenticatedException)
def handle_auth_exception(request: Request, exc: NotAuthenticatedException) -> Response:
    """Redirect the user to the login page if not logged in."""
    return auth_exception_handler(request, exc)


app.mount("/static", StaticFiles(directory="tronbyt_server/static"), name="static")

app.include_router(api.router)
app.include_router(auth.router)
app.include_router(manager.router)
app.include_router(websockets.router)
