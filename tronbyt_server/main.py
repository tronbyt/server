"""Main application file."""

import logging
from pathlib import Path


from fastapi import FastAPI, Request
from fastapi.middleware.gzip import GZipMiddleware
from fastapi.responses import Response
from fastapi.staticfiles import StaticFiles
from fastapi_babel import Babel, BabelConfigs, BabelMiddleware
from starlette.middleware.sessions import SessionMiddleware

from tronbyt_server.config import get_settings
from tronbyt_server.dependencies import (
    NotAuthenticatedException,
    auth_exception_handler,
)
from tronbyt_server.routers import api, auth, manager, websockets
from tronbyt_server.templates import templates

MODULE_ROOT = Path(__file__).parent.resolve()
logger = logging.getLogger(__name__)


app = FastAPI()


app.add_middleware(GZipMiddleware, minimum_size=1000)
app.add_middleware(SessionMiddleware, secret_key=get_settings().SECRET_KEY)

# Babel configuration
babel_configs = BabelConfigs(
    ROOT_DIR=MODULE_ROOT.parent,
    BABEL_DEFAULT_LOCALE="en",
    BABEL_TRANSLATION_DIRECTORY=str(MODULE_ROOT / "translations"),
)
app.add_middleware(
    BabelMiddleware, babel_configs=babel_configs, jinja2_templates=templates
)
Babel(configs=babel_configs)


@app.exception_handler(NotAuthenticatedException)
def handle_auth_exception(request: Request, exc: NotAuthenticatedException) -> Response:
    """Redirect the user to the login page if not logged in."""
    return auth_exception_handler(request, exc)


app.mount("/static", StaticFiles(directory=MODULE_ROOT / "static"), name="static")

app.include_router(api.router)
app.include_router(auth.router)
app.include_router(manager.router)
app.include_router(websockets.router)
