"""Authentication router."""

import secrets
import sqlite3
import string
import time
from typing import Annotated, Optional

from fastapi import APIRouter, Depends, Form, Request, status
from fastapi.responses import Response, RedirectResponse
from pydantic import BaseModel
from werkzeug.security import generate_password_hash

import tronbyt_server.db as db
from tronbyt_server.config import settings
from tronbyt_server.dependencies import get_db, manager
from tronbyt_server.flash import flash
from tronbyt_server.models.user import User
from tronbyt_server.templates import templates

router = APIRouter(prefix="/auth", tags=["auth"])


def _generate_api_key() -> str:
    """Generate a random API key."""
    return "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )


@router.get("/register_owner")
def get_register_owner(
    request: Request, db_conn: sqlite3.Connection = Depends(get_db)
) -> Response:
    """Render the owner registration page."""
    if db.has_users(db_conn):
        return RedirectResponse(url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND)
    return templates.TemplateResponse(request, "auth/register_owner.html")


@router.post("/register_owner")
def post_register_owner(
    request: Request,
    password: str = Form(...),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle owner registration."""
    if db.has_users(db_conn):
        return RedirectResponse(url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND)

    if not password:
        flash(request, "Password is required.")
    else:
        username = "admin"
        api_key = _generate_api_key()
        user = User(
            username=username,
            password=generate_password_hash(password),
            api_key=api_key,
        )
        if db.save_user(db_conn, user.model_dump(), new_user=True):
            db.create_user_dir(username)
            flash(request, "Admin user created. Please log in.")
            return RedirectResponse(
                url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
            )
        else:
            flash(request, "Could not create admin user.")

    return templates.TemplateResponse(request, "auth/register_owner.html")


@router.get("/register")
def get_register(
    request: Request,
    user: Optional[User] = Depends(manager.optional),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Render the user registration page."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url="/auth/register_owner", status_code=status.HTTP_302_FOUND
        )
    if settings.ENABLE_USER_REGISTRATION != "1":
        if not user or user.username != "admin":
            flash(request, "User registration is not enabled.")
            return RedirectResponse(
                url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
            )
    return templates.TemplateResponse(
        request, "auth/register.html", {"user": user}
    )


@router.post("/register")
def post_register(
    request: Request,
    username: str = Form(...),
    password: str = Form(...),
    email: str = Form(""),
    user: Optional[User] = Depends(manager.optional),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle user registration."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_register_owner"), status_code=status.HTTP_302_FOUND
        )

    if settings.ENABLE_USER_REGISTRATION != "1":
        if not user or user.username != "admin":
            flash(request, "User registration is not enabled.")
            return RedirectResponse(
                url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
            )

    max_users = settings.MAX_USERS
    if max_users > 0 and len(db.get_all_users(db_conn)) >= max_users:
        flash(
            request, "Maximum number of users reached. Registration is disabled."
        )
        return RedirectResponse(url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND)

    error = None
    status_code = status.HTTP_200_OK
    if not username:
        error = "Username is required."
        status_code = status.HTTP_400_BAD_REQUEST
    elif not password:
        error = "Password is required."
        status_code = status.HTTP_400_BAD_REQUEST
    elif db.get_user(db_conn, username):
        error = "User is already registered."
        status_code = status.HTTP_409_CONFLICT

    if error is None:
        api_key = _generate_api_key()
        new_user = User(
            username=username,
            password=generate_password_hash(password),
            email=email,
            api_key=api_key,
        )
        if db.save_user(db_conn, new_user.model_dump(), new_user=True):
            db.create_user_dir(username)
            if user and user.username == "admin":
                flash(request, f"User {username} registered successfully.")
                return RedirectResponse(
                    url="/auth/register", status_code=status.HTTP_302_FOUND
                )
            else:
                flash(request, f"Registered as {username}.")
                return RedirectResponse(
                    url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
                )
        else:
            error = "Couldn't Save User"
            status_code = status.HTTP_500_INTERNAL_SERVER_ERROR
    if error:
        flash(request, error)
    return templates.TemplateResponse(
        request, "auth/register.html", {"user": user}, status_code=status_code
    )


@router.get("/login")
def get_login(request: Request, db_conn: sqlite3.Connection = Depends(get_db)) -> Response:
    """Render the login page."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_register_owner"), status_code=status.HTTP_302_FOUND
        )
    return templates.TemplateResponse(
        request, "auth/login.html", {"config": settings}
    )


@router.post("/login")
def post_login(
    request: Request,
    username: str = Form(...),
    password: str = Form(...),
    remember: Optional[str] = Form(None),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle user login."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_register_owner"), status_code=status.HTTP_302_FOUND
        )

    user_data = db.auth_user(db_conn, username, password)
    if not isinstance(user_data, dict):
        flash(request, "Incorrect username/password.")
        if not settings.PRODUCTION == "0":
            time.sleep(2)
        return templates.TemplateResponse(
            request, "auth/login.html", {"config": settings}
        )

    user = User(**user_data)
    response = RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)
    access_token = manager.create_access_token(data={"sub": user.username})
    manager.set_cookie(response, access_token)
    return response


@router.get("/edit")
def get_edit(request: Request, user: User = Depends(manager)) -> Response:
    """Render the edit user page."""
    firmware_version = None
    if user and user.username == "admin":
        firmware_version = db.get_firmware_version()
    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {"user": user, "firmware_version": firmware_version},
    )


@router.post("/edit")
def post_edit(
    request: Request,
    old_password: str = Form(...),
    password: str = Form(...),
    user: User = Depends(manager),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle user edit."""
    authed_user_data = db.auth_user(db_conn, user.username, old_password)
    if not authed_user_data:
        flash(request, "Bad old password.")
    else:
        authed_user = User(**authed_user_data)
        authed_user.password = generate_password_hash(password)
        db.save_user(db_conn, authed_user.model_dump())
        flash(request, "Success")
        return RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    firmware_version = None
    if user and user.username == "admin":
        firmware_version = db.get_firmware_version()
    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {"user": user, "firmware_version": firmware_version},
    )


@router.get("/logout")
def logout(request: Request) -> Response:
    """Log the user out."""
    flash(request, "Logged Out")
    response = RedirectResponse(url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND)
    response.delete_cookie("session")
    return response


class ThemePreference(BaseModel):
    """Pydantic model for theme preference."""

    theme: str


@router.post("/set_theme_preference")
def set_theme_preference(
    preference: ThemePreference,
    user: Annotated[User, Depends(manager)],
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Set the theme preference for a user."""
    if preference.theme not in ["light", "dark", "system"]:
        return {"status": "error", "message": "Invalid theme value"}, 400

    user.theme_preference = preference.theme
    if db.save_user(db_conn, user.model_dump()):
        return {"status": "success", "message": "Theme preference updated"}
    else:
        return {
            "status": "error",
            "message": "Failed to save theme preference",
        }, 500
