"""FastAPI dependencies."""

import sqlite3
from typing import Generator, Optional

from fastapi import Depends, Header, Request
from fastapi.responses import RedirectResponse, Response
from fastapi_login import LoginManager
from fastapi_login.exceptions import InvalidCredentialsException

from tronbyt_server import db
from tronbyt_server.config import settings
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
    db_conn = sqlite3.connect(settings.DB_FILE)
    with db_conn:
        yield db_conn


def get_user_from_api_key(
    authorization: Optional[str] = Header(None, alias="Authorization"),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> dict:
    """Get a user from an API key."""
    if not authorization:
        raise InvalidCredentialsException

    if authorization.startswith("Bearer "):
        api_key = authorization.split(" ")[1]
    else:
        api_key = authorization

    user = db.get_user_by_api_key(db_conn, api_key)
    if not user:
        raise InvalidCredentialsException
    return user


@manager.user_loader()
def load_user(username: str) -> Optional[User]:
    """Load a user from the database."""
    with next(get_db()) as db_conn:
        user_data = db.get_user(db_conn, username)
        if user_data:
            return User(**user_data)
        return None


def auth_exception_handler(request: Request, exc: NotAuthenticatedException) -> Response:
    """
    Redirect the user to the login page if not logged in.
    """
    return RedirectResponse(request.url_for("get_login"))
