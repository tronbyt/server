"""FastAPI dependencies."""

import sqlite3
from typing import Generator

from fastapi import Depends, Header, Request
from fastapi.responses import RedirectResponse, Response
from fastapi_login import LoginManager
from fastapi_login.exceptions import InvalidCredentialsException

from tronbyt_server import db
from tronbyt_server.config import settings
from tronbyt_server.models.device import Device
from tronbyt_server.models.user import User


class NotAuthenticatedException(Exception):
    """Exception for when a user is not authenticated."""

    pass


manager = LoginManager(
    settings.SECRET_KEY,
    "/auth/login",
    use_cookie=True,
    not_authenticated_exception=NotAuthenticatedException,
)


def get_db() -> Generator[sqlite3.Connection, None, None]:
    """Get a database connection."""
    db_conn = sqlite3.connect(settings.DB_FILE, check_same_thread=False)
    with db_conn:
        yield db_conn


def check_for_users(
    request: Request, db_conn: sqlite3.Connection = Depends(get_db)
) -> None:
    """Check if there are any users in the database."""
    if not db.has_users(db_conn):
        if request.url.path != "/auth/register_owner":
            raise NotAuthenticatedException


def get_user_and_device_from_api_key(
    device_id: str | None = None,
    authorization: str | None = Header(None, alias="Authorization"),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> tuple[User | None, Device | None]:
    """Get a user and/or device from an API key."""
    if not authorization:
        raise InvalidCredentialsException

    api_key = (
        authorization.split(" ")[1]
        if authorization.startswith("Bearer ")
        else authorization
    )

    user = db.get_user_by_api_key(db_conn, api_key)
    if user:
        device = user.devices.get(device_id) if device_id else None
        return user, device

    device = db.get_device_by_id(db_conn, device_id) if device_id else None
    if device and device.api_key == api_key:
        user = db.get_user_by_device_id(db_conn, device.id)
        return user, device

    raise InvalidCredentialsException


@manager.user_loader()  # type: ignore
def load_user(username: str) -> User | None:
    """Load a user from the database."""
    with next(get_db()) as db_conn:
        user = db.get_user(db_conn, username)
        if user:
            return user
        return None


def auth_exception_handler(
    request: Request, exc: NotAuthenticatedException
) -> Response:
    """
    Redirect the user to the login page if not logged in.
    """
    with next(get_db()) as db_conn:
        if not db.has_users(db_conn):
            return RedirectResponse(request.url_for("get_register_owner"))
    return RedirectResponse(request.url_for("get_login"))
