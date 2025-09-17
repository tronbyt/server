import os
import secrets
import string
import time
from http import HTTPStatus
from typing import Optional

import secrets
import string
import time
from http import HTTPStatus
from typing import Optional

from typing import Optional
from fastapi import APIRouter, Cookie, Depends, Form, Request, HTTPException
from fastapi.responses import HTMLResponse, RedirectResponse
from werkzeug.security import generate_password_hash
from werkzeug.utils import secure_filename

import tronbyt_server.db_fastapi as db
from tronbyt_server.templating import templates
from tronbyt_server.main import logger
from tronbyt_server.models_fastapi import User

router = APIRouter(prefix="/auth")


async def get_current_user(session: Optional[str] = Cookie(None)) -> Optional[User]:
    if session:
        user = db.get_user(logger, session)
        if user:
            return User(**user)
    return None


async def login_required(
    request: Request, current_user: Optional[User] = Depends(get_current_user)
) -> User:
    if not current_user:
        raise HTTPException(
            status_code=HTTPStatus.FOUND,
            headers={"Location": str(request.url_for("login_get"))},
        )
    return current_user


def _generate_api_key() -> str:
    """Generate a random API key."""
    return "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )


@router.get("/login", name="login_get", response_class=HTMLResponse)
async def login_get(
    request: Request, current_user: Optional[User] = Depends(get_current_user)
):
    if not db.has_users():
        return RedirectResponse(
            url=request.url_for("register_owner_get"), status_code=HTTPStatus.SEE_OTHER
        )
    return templates.TemplateResponse(
        request,
        "auth/login.html",
        {
            "request": request,
            "config": {},
            "user": current_user,
        },
    )


@router.post("/login", name="login_post")
async def login_post(
    request: Request,
):
    if not db.has_users():
        return RedirectResponse(
            url=request.url_for("register_owner_get"), status_code=HTTPStatus.SEE_OTHER
        )

    form = await request.form()
    username = form.get("username")
    password = form.get("password")
    error = None
    user = db.auth_user(logger, username, password)
    if user is False or user is None:
        error = "Incorrect username/password."
        time.sleep(2)  # slow down brute force attacks

    if error is None and user:
        response = RedirectResponse(
            url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
        )
        response.set_cookie(key="session", value=user["username"], httponly=True)
        return response

    session = request.cookies.get("session")
    current_user = await get_current_user(session)
    return templates.TemplateResponse(
        request,
        "auth/login.html",
        {
            "request": request,
            "config": {},
            "user": current_user,
            "error": error,
        },
    )


@router.get("/register_owner", name="register_owner_get", response_class=HTMLResponse)
async def register_owner_get(request: Request):
    if db.has_users():
        return RedirectResponse(
            url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
        )

    return templates.TemplateResponse(
        request, "auth/register_owner.html", {"request": request}
    )


@router.post("/register_owner", name="register_owner_post")
async def register_owner_post(request: Request):
    if db.has_users():
        return RedirectResponse(
            url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
        )

    form = await request.form()
    password = form.get("password")
    if not password:
        # flash("Password is required.")
        return templates.TemplateResponse(
            request, "auth/register_owner.html", {"request": request, "error": "Password is required."}
        )
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
            # flash("Admin user created. Please log in.")
            return RedirectResponse(
                url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
            )
        else:
            # flash("Could not create admin user.")
            return templates.TemplateResponse(
                request, "auth/register_owner.html", {"request": request, "error": "Could not create admin user."}
            )


@router.get("/register", response_class=HTMLResponse, name="register_get")
async def register_get(
    request: Request, current_user: Optional[User] = Depends(get_current_user)
):
    if not db.has_users():
        return RedirectResponse(
            url=request.url_for("register_owner_get"), status_code=HTTPStatus.SEE_OTHER
        )
    if os.getenv("ENABLE_USER_REGISTRATION", "0") != "1":
        if not current_user or current_user.username != "admin":
            return RedirectResponse(
                url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
            )
    return templates.TemplateResponse(
        request, "auth/register.html", {"request": request, "user": current_user}
    )


@router.post("/register", response_class=HTMLResponse, name="register_post")
async def register_post(
    request: Request, current_user: Optional[User] = Depends(get_current_user)
):
    max_users = int(os.getenv("MAX_USERS", "100"))
    if max_users > 0:
        users_count = len(db.get_all_users(logger))
        if users_count >= max_users:
            return RedirectResponse(
                url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
            )

    error = None
    form = await request.form()
    username = secure_filename(form.get("username"))
    if username != form.get("username"):
        error = "Invalid Username"
    password = form.get("password")

    if not username:
        error = "Username is required."
    elif not password:
        error = "Password is required."
    if db.get_user(logger, username):
        error = "User is already registered."

    if error is None:
        email = "none"
        if "email" in form:
            if "@" in form.get("email"):
                email = form.get("email")
        api_key = _generate_api_key()
        user = User(
            username=username,
            password=generate_password_hash(password),
            email=email,
            api_key=api_key,
            theme_preference="system",
        )

        if db.save_user(logger, user.model_dump(), new_user=True):
            db.create_user_dir(username)
            if current_user and current_user.username == "admin":
                # flash(f"User {username} registered successfully.")
                return RedirectResponse(
                    url=request.url_for("register_get"),
                    status_code=HTTPStatus.SEE_OTHER,
                )
            else:
                # flash(f"Registered as {username}.")
                return RedirectResponse(
                    url=request.url_for("login_get"),
                    status_code=HTTPStatus.SEE_OTHER,
                )
        else:
            error = "Couldn't Save User"
    # flash(error)
    return templates.TemplateResponse(
        request,
        "auth/register.html",
        {"request": request, "user": current_user, "error": error},
    )


@router.get("/edit", response_class=HTMLResponse, name="edit")
async def edit_get(request: Request, current_user: User = Depends(login_required)):
    firmware_version = None
    if current_user and current_user.username == "admin":
        firmware_version = db.get_firmware_version(logger)
    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {
            "request": request,
            "user": current_user,
            "firmware_version": firmware_version,
        },
    )


@router.post("/edit", name="edit_post")
async def edit_post(request: Request, current_user: User = Depends(login_required)):
    form = await request.form()
    username = current_user.username
    old_pass = form.get("old_password")
    password = form.get("password")
    error = None
    user = db.auth_user(logger, username, old_pass)
    if user is False or user is None:
        error = "Bad old password."
    if error is None:
        user_data = current_user.model_dump()
        user_data["password"] = generate_password_hash(password)
        db.save_user(logger, user_data)
        # flash("Success")
        return RedirectResponse(
            url=request.url_for("index"), status_code=HTTPStatus.SEE_OTHER
        )
    # flash(error)
    firmware_version = None
    if current_user and current_user.username == "admin":
        firmware_version = db.get_firmware_version(logger)
    return templates.TemplateResponse(
        request,
        "auth/edit.html",
        {
            "request": request,
            "user": current_user,
            "error": error,
            "firmware_version": firmware_version,
        },
    )


@router.get("/logout", name="logout")
async def logout(request: Request):
    response = RedirectResponse(
        url=request.url_for("login_get"), status_code=HTTPStatus.SEE_OTHER
    )
    response.delete_cookie("session")
    # flash("Logged Out")
    return response


@router.post("/set_theme_preference")
async def set_theme_preference(
    data: dict, current_user: User = Depends(login_required)
):
    if not data or "theme" not in data:
        return {"status": "error", "message": "Missing theme data"}, 400

    theme = data["theme"]
    valid_themes = ["light", "dark", "system"]
    if theme not in valid_themes:
        return {"status": "error", "message": "Invalid theme value"}, 400

    user = current_user.model_dump()
    user["theme_preference"] = theme
    if db.save_user(logger, user):
        return {"status": "success", "message": "Theme preference updated"}
    else:
        return {
            "status": "error",
            "message": "Failed to save theme preference",
        }, 500
