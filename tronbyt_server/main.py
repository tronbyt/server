"""Main application file."""

import logging
import shutil
import sqlite3
from contextlib import asynccontextmanager
from datetime import datetime
from pathlib import Path

from typing import AsyncGenerator

from fastapi import FastAPI, Request
from fastapi.responses import Response
from fastapi.staticfiles import StaticFiles
from fastapi_babel import Babel, BabelConfigs, BabelMiddleware
from starlette.middleware.sessions import SessionMiddleware

from tronbyt_server import db
from tronbyt_server.config import get_settings
from tronbyt_server.dependencies import (
    NotAuthenticatedException,
    auth_exception_handler,
    get_db,
)
from tronbyt_server.routers import api, auth, manager, websockets
from tronbyt_server.templates import templates

MODULE_ROOT = Path(__file__).parent.resolve()


def backup_database(db_file: str, logger: logging.Logger) -> None:
    """Create a timestamped backup of the SQLite database."""
    db_path = Path(db_file)
    if not db_path.exists():
        logger.warning(f"Database file does not exist, skipping backup: {db_file}")
        return

    # Create backup directory if it doesn't exist
    backup_dir = db_path.parent / "backups"
    backup_dir.mkdir(exist_ok=True)

    # Get schema version from database
    schema_version = "unknown"
    try:
        conn = sqlite3.connect(db_file)
        cursor = conn.cursor()
        cursor.execute("SELECT schema_version FROM meta LIMIT 1")
        row = cursor.fetchone()
        if row:
            schema_version = str(row[0])
        conn.close()
    except Exception as e:
        logger.warning(f"Could not retrieve schema version: {e}")

    # Create timestamped backup filename with schema version
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    backup_filename = f"{db_path.stem}_{timestamp}_v{schema_version}.db"
    backup_path = backup_dir / backup_filename

    try:
        shutil.copy2(db_file, backup_path)
        logger.info(f"Database backed up to: {backup_path}")
    except Exception as e:
        logger.error(f"Failed to backup database: {e}")


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncGenerator[None, None]:
    """Run startup and shutdown events."""
    # Startup
    logging.basicConfig(level=get_settings().LOG_LEVEL)
    logger = logging.getLogger(__name__)

    # Backup the database before initializing (only in production)
    settings = get_settings()
    if settings.PRODUCTION == "1":
        backup_database(settings.DB_FILE, logger)
    else:
        logger.info("Development mode - skipping database backup")

    db_connection = next(get_db(settings=settings))
    with db_connection:
        db.init_db(db_connection)
    yield
    # Shutdown
    from tronbyt_server.sync import get_sync_manager

    get_sync_manager(logger).shutdown()


app = FastAPI(lifespan=lifespan)


app.add_middleware(SessionMiddleware, secret_key=get_settings().SECRET_KEY)

# Babel configuration
babel_configs = BabelConfigs(
    ROOT_DIR=MODULE_ROOT.parent,
    BABEL_DEFAULT_LOCALE="en",
    BABEL_TRANSLATION_DIRECTORY=MODULE_ROOT / "translations",
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
