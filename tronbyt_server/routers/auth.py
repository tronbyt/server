"""Authentication router."""

import secrets
import sqlite3
import string
import time
from datetime import timedelta
from typing import Annotated, cast, Literal

from fastapi import APIRouter, Depends, Form, Request, status
from fastapi.responses import Response, RedirectResponse, JSONResponse
from pydantic import BaseModel
from werkzeug.security import generate_password_hash

import tronbyt_server.db as db
from tronbyt_server import system_apps, version
from tronbyt_server.config import Settings, get_settings
from tronbyt_server.dependencies import get_db, manager
from tronbyt_server.flash import flash
from tronbyt_server.models import User
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
        return RedirectResponse(
            url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
        )
    return templates.TemplateResponse(request, "auth/register_owner.html")


@router.post("/register_owner")
def post_register_owner(
    request: Request,
    password: str = Form(...),
    db_conn: sqlite3.Connection = Depends(get_db),
) -> Response:
    """Handle owner registration."""
    if db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
        )

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
        if db.save_user(db_conn, user, new_user=True):
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
    user: User | None = Depends(manager.optional),
    db_conn: sqlite3.Connection = Depends(get_db),
    settings: Settings = Depends(get_settings),
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
    return templates.TemplateResponse(request, "auth/register.html", {"user": user})


class RegisterFormData(BaseModel):
    """Represents the form data for user registration."""

    username: str
    password: str
    email: str = ""


@router.post("/register")
def post_register(
    request: Request,
    form_data: Annotated[RegisterFormData, Form()],
    user: User | None = Depends(manager.optional),
    db_conn: sqlite3.Connection = Depends(get_db),
    settings: Settings = Depends(get_settings),
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
        flash(request, "Maximum number of users reached. Registration is disabled.")
        return RedirectResponse(
            url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
        )

    error = None
    status_code = status.HTTP_200_OK
    if not form_data.username:
        error = "Username is required."
        status_code = status.HTTP_400_BAD_REQUEST
    elif not form_data.password:
        error = "Password is required."
        status_code = status.HTTP_400_BAD_REQUEST
    elif db.get_user(db_conn, form_data.username):
        error = "User is already registered."
        status_code = status.HTTP_409_CONFLICT

    if error is None:
        api_key = _generate_api_key()
        new_user = User(
            username=form_data.username,
            password=generate_password_hash(form_data.password),
            email=form_data.email,
            api_key=api_key,
        )
        if db.save_user(db_conn, new_user, new_user=True):
            db.create_user_dir(form_data.username)
            if user and user.username == "admin":
                flash(request, f"User {form_data.username} registered successfully.")
                return RedirectResponse(
                    url="/auth/register", status_code=status.HTTP_302_FOUND
                )
            else:
                flash(request, f"Registered as {form_data.username}.")
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
def get_login(
    request: Request,
    db_conn: sqlite3.Connection = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> Response:
    """Render the login page."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_register_owner"), status_code=status.HTTP_302_FOUND
        )
    return templates.TemplateResponse(request, "auth/login.html", {"config": settings})


class LoginFormData(BaseModel):
    """Represents the form data for user login."""

    username: str
    password: str
    remember: str | None = None


@router.post("/login")
def post_login(
    request: Request,
    form_data: Annotated[LoginFormData, Form()],
    db_conn: sqlite3.Connection = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> Response:
    """Handle user login."""
    if not db.has_users(db_conn):
        return RedirectResponse(
            url=request.url_for("get_register_owner"), status_code=status.HTTP_302_FOUND
        )

    user_data = db.auth_user(db_conn, form_data.username, form_data.password)
    if not isinstance(user_data, User):
        flash(request, "Incorrect username/password.")
        if not settings.PRODUCTION == "0":
            time.sleep(2)
        return templates.TemplateResponse(
            request,
            "auth/login.html",
            {"config": settings},
            status_code=status.HTTP_401_UNAUTHORIZED,
        )

    user = user_data
    response = RedirectResponse(url="/", status_code=status.HTTP_302_FOUND)

    # Set token expiration
    token_expires = timedelta(days=30) if form_data.remember else timedelta(minutes=60)
    access_token = manager.create_access_token(
        data={"sub": user.username}, expires=token_expires
    )

    # Set cookie expiration on the browser
    cookie_max_age = (
        30 * 24 * 60 * 60 if form_data.remember else None
    )  # 30 days or session
    response.set_cookie(
        key=manager.cookie_name,
        value=access_token,
        max_age=cookie_max_age,
        secure=settings.SERVER_PROTOCOL == "https",  # Set secure flag in production
        httponly=True,  # Standard security practice
        samesite="lax",  # Can be "strict" or "lax"
    )

    return response


@router.get("/edit")
def get_edit(request: Request, user: User = Depends(manager)) -> Response:
    """Render the edit user page."""
    firmware_version = None
    system_repo_info = None
    if user and user.username == "admin":
        firmware_version = db.get_firmware_version()
        system_repo_info = system_apps.get_system_repo_info(db.get_data_dir())

    # Get server version info for all users
    server_version_info = version.get_version_info()

    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {
            "user": user,
            "firmware_version": firmware_version,
            "system_repo_info": system_repo_info,
            "server_version_info": server_version_info,
        },
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
    if not isinstance(authed_user_data, User):
        flash(request, "Bad old password.")
    else:
        authed_user = authed_user_data
        authed_user.password = generate_password_hash(password)
        db.save_user(db_conn, authed_user)
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
    response = RedirectResponse(
        url=request.url_for("get_login"), status_code=status.HTTP_302_FOUND
    )
    response.delete_cookie(manager.cookie_name)
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
        return JSONResponse(
            content={"status": "error", "message": "Invalid theme value"},
            status_code=status.HTTP_400_BAD_REQUEST,
        )

    user.theme_preference = cast(Literal["light", "dark", "system"], preference.theme)
    if db.save_user(db_conn, user):
        return JSONResponse(
            content={"status": "success", "message": "Theme preference updated"}
        )
    else:
        return JSONResponse(
            content={
                "status": "error",
                "message": "Failed to save theme preference",
            },
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
        )
