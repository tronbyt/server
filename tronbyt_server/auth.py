"""
This module provides authentication and user management endpoints.

It includes endpoints for user registration, login, logout, and editing user details.
"""
import functools
import secrets
import string
import time
from typing import Any, Callable, Optional

from fastapi import APIRouter, Depends, Form, HTTPException, Request, Response
from fastapi.responses import RedirectResponse
from werkzeug.security import generate_password_hash
from werkzeug.utils import secure_filename

from tronbyt_server import db
from tronbyt_server.models.user import User
from tronbyt_server.templating import templates
from tronbyt_server.main import logger
from tronbyt_server.config import get_settings, Settings
from tronbyt_server.flash import flash

router = APIRouter()


def _generate_api_key() -> str:
    """Generate a random API key."""
    return "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )


def get_current_user(request: Request) -> Optional[User]:
    username = request.session.get("username")
    if username:
        user_data = db.get_user(logger, username)
        if user_data:
            return User(**user_data)
    return None


def login_required(current_user: User = Depends(get_current_user)):
    if not current_user:
        raise HTTPException(status_code=302, detail="Not authenticated")
    return current_user


async def get_user_from_api_key(
    authorization: Optional[str] = Depends(login_required),
) -> User:
    if not authorization:
        raise HTTPException(status_code=401, detail="Missing API key")
    user_data = db.get_user_by_api_key(logger, authorization)
    if not user_data:
        raise HTTPException(status_code=401, detail="Invalid API key")
    return User(**user_data)


@router.get("/register_owner")
@router.post("/register_owner")
async def register_owner(request: Request):
    if db.has_users():
        # If users already exist, redirect to the login page
        return RedirectResponse(url=router.url_path_for("login"), status_code=303)

    if request.method == "POST":
        form = await request.form()
        password = form.get("password")
        if not password:
            flash(request, "Password is required.")
            pass
        else:
            username = "admin"
            api_key = _generate_api_key()
            user = User(
                username=username,
                password=generate_password_hash(password),
                email="",
                api_key=api_key,
                theme_preference="system",
            )
            if db.save_user(logger, user.model_dump(), new_user=True):
                db.create_user_dir(username)
                flash(request, "Admin user created. Please log in.")
                return RedirectResponse(
                    url=router.url_path_for("login"), status_code=303
                )
            else:
                flash(request, "Could not create admin user.")
                pass

    return templates.TemplateResponse(request, "auth/register_owner.html", {"user": None})


@router.get("/register")
@router.post("/register")
async def register(request: Request, current_user: Optional[User] = Depends(get_current_user), settings: Settings = Depends(get_settings)):
    if not db.has_users():
        return RedirectResponse(
            url=router.url_path_for("register_owner"), status_code=303
        )
    # Check if user registration is enabled for non-authenticated users
    if settings.enable_user_registration != "1":
        # Only allow admin to register new users if open registration is disabled
        if not current_user or current_user.username != "admin":
            flash(request, "User registration is not enabled.")
            return RedirectResponse(url=router.url_path_for("login"), status_code=303)

    # Check if max users limit is reached
    if settings.max_users > 0:
        users_count = len(db.get_all_users(logger))
        if users_count >= settings.max_users:
            flash(request, "Maximum number of users reached. Registration is disabled.")
            return RedirectResponse(url=router.url_path_for("login"), status_code=303)

    if request.method == "POST":
        error: Optional[str] = None
        form = await request.form()
        username = secure_filename(form.get("username", ""))
        if username != form.get("username", ""):
            error = "Invalid Username"
        password = generate_password_hash(form.get("password", ""))

        if not username:
            error = "Username is required."
        elif not password:
            error = "Password is required."
        if error is not None and db.get_user(logger, username):
            error = "User is already registered."
        if error is None:
            email = "none"
            if "email" in form:
                if "@" in form.get("email", ""):
                    email = form.get("email", "")
            api_key = _generate_api_key()
            user = User(
                username=username,
                password=password,
                email=email,
                api_key=api_key,
                theme_preference="system",  # Default theme for new users
            )

            if db.save_user(logger, user.model_dump(), new_user=True):
                db.create_user_dir(username)
                # Only redirect to login if not admin registering another user
                if current_user and current_user.username == "admin":
                    # Admin registered a new user, stay on the registration page
                    flash(request, f"User {username} registered successfully.")
                    return RedirectResponse(
                        url=router.url_path_for("register"), status_code=303
                    )
                else:  # Non-admin user registered, redirect to login
                    flash(request, f"Registered as {username}.")
                    return RedirectResponse(
                        url=router.url_path_for("login"), status_code=303
                    )
            else:
                error = "Couldn't Save User"
        flash(request, error)
    return templates.TemplateResponse(request, "auth/register.html", {"user": current_user})


@router.get("/login")
@router.post("/login")
async def login(request: Request):
    if not db.has_users():
        return RedirectResponse(
            url=router.url_path_for("register_owner"), status_code=303
        )
    if request.method == "POST":
        form = await request.form()
        username = form.get("username", "")
        password = form.get("password", "")
        logger.debug(f"username : {username} and hp : {password}")
        error: Optional[str] = None
        user = db.auth_user(logger, username, password)
        if user is False or user is None:
            error = "Incorrect username/password."
            # if not current_app.config["TESTING"]:
            #     time.sleep(2)  # slow down brute force attacks
        if error is None:
            request.session.clear()
            remember_me = form.get("remember")
            request.session["permanent"] = bool(remember_me)

            logger.debug("username " + username)
            request.session["username"] = username
            return RedirectResponse(url="/", status_code=303)
        flash(request, error)

    return templates.TemplateResponse(
        request, "auth/login.html", {"user": request.session.get("user")}
    )


@router.get("/edit")
@router.post("/edit")
async def edit(request: Request, current_user: User = Depends(login_required)):
    if request.method == "POST":
        form = await request.form()
        username = request.session["username"]
        old_pass = form.get("old_password", "")
        password = generate_password_hash(form.get("password", ""))
        error: Optional[str] = None
        user_data = db.auth_user(logger, username, old_pass)
        if user_data is False:
            error = "Bad old password."
        if error is None:
            if isinstance(user_data, dict):
                user_data["password"] = password
                db.save_user(logger, user_data)
            flash(request, "Success")
            return RedirectResponse(url="/", status_code=303)
        flash(request, error)

    firmware_version = None
    if current_user and current_user.username == "admin":
        firmware_version = db.get_firmware_version(logger)
    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {"user": current_user, "firmware_version": firmware_version},
    )


@router.get("/logout")
async def logout(request: Request):
    request.session.clear()
    flash(request, "Logged Out")
    return RedirectResponse(url=router.url_path_for("login"), status_code=303)


@router.post("/set_theme_preference")
async def set_theme_preference(
    request: Request, current_user: User = Depends(login_required)
):
    data = await request.json()
    if not data or "theme" not in data:
        return {"status": "error", "message": "Missing theme data"}, 400

    theme = data["theme"]
    valid_themes = ["light", "dark", "system"]
    if theme not in valid_themes:
        return {"status": "error", "message": "Invalid theme value"}, 400

    current_user.theme_preference = theme
    if db.save_user(logger, current_user.model_dump()):
        logger.info(f"User {current_user.username} set theme to {theme}")
        return {"status": "success", "message": "Theme preference updated"}
    else:
        logger.error(f"Failed to save theme for user {current_user.username}")
        return {"status": "error", "message": "Failed to save theme preference"}, 500
