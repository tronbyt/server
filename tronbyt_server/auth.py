"""Authentication blueprint for handling user registration, login, and management."""

import functools
import secrets
import string
import time
from typing import Any, Callable, Optional

from flask import (
    Blueprint,
    current_app,
    flash,
    g,
    redirect,
    render_template,
    request,
    session,
    url_for,
)
from flask.typing import ResponseReturnValue
from werkzeug.security import generate_password_hash
from werkzeug.utils import secure_filename

import tronbyt_server.db as db
from tronbyt_server import system_apps
from tronbyt_server.models.user import User

bp = Blueprint("auth", __name__, url_prefix="/auth")


def _generate_api_key() -> str:
    """Generate a random API key."""
    return "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )


@bp.route("/register_owner", methods=("GET", "POST"))
def register_owner() -> ResponseReturnValue:
    if db.has_users():
        # If users already exist, redirect to the login page
        return redirect(url_for("auth.login"))

    if request.method == "POST":
        password = request.form["password"]
        if not password:
            flash("Password is required.")
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
            if db.save_user(user, new_user=True):
                db.create_user_dir(username)
                flash("Admin user created. Please log in.")
                return redirect(url_for("auth.login"))
            else:
                flash("Could not create admin user.")

    return render_template("auth/register_owner.html")


@bp.route("/register", methods=("GET", "POST"))
def register() -> ResponseReturnValue:
    if not db.has_users():
        return redirect(url_for("auth.register_owner"))
    # Check if user registration is enabled for non-authenticated users
    if current_app.config.get("ENABLE_USER_REGISTRATION") != "1":
        # Only allow admin to register new users if open registration is disabled
        if not g.user or g.user.get("username") != "admin":
            flash("User registration is not enabled.")
            return redirect(url_for("auth.login"))

    # Check if max users limit is reached
    max_users = current_app.config.get("MAX_USERS", 100)  # Default to 100
    if max_users > 0:
        users_count = len(db.get_all_users())
        if users_count >= max_users:
            flash("Maximum number of users reached. Registration is disabled.")
            return redirect(url_for("auth.login"))

    if request.method == "POST":
        error: Optional[str] = None

        username = secure_filename(request.form["username"])
        if username != request.form["username"]:
            error = "Invalid Username"
        password = generate_password_hash(request.form["password"])

        if not username:
            error = "Username is required."
        elif not password:
            error = "Password is required."
        if error is not None and db.get_user(username):
            error = "User is already registered."
        if error is None:
            email = "none"
            if "email" in request.form:
                if "@" in request.form["email"]:
                    email = request.form["email"]
            api_key = _generate_api_key()
            user = User(
                username=username,
                password=password,
                email=email,
                api_key=api_key,
                theme_preference="system",  # Default theme for new users
            )

            if db.save_user(user, new_user=True):
                db.create_user_dir(username)
                # Only redirect to login if not admin registering another user
                if (
                    g.user and g.user.get("username") == "admin"
                ):  # Admin registered a new user, stay on the registration page
                    flash(f"User {username} registered successfully.")
                    return redirect(url_for("auth.register"))
                else:  # Non-admin user registered, redirect to login
                    flash(f"Registered as {username}.")
                    return redirect(url_for("auth.login"))
            else:
                error = "Couldn't Save User"
        flash(error)
    return render_template("auth/register.html")


@bp.route("/login", methods=("GET", "POST"))
def login() -> ResponseReturnValue:
    if not db.has_users():
        return redirect(url_for("auth.register_owner"))
    if request.method == "POST":
        username = request.form["username"]
        password = request.form["password"]
        current_app.logger.debug(f"username : {username} and hp : {password}")
        error: Optional[str] = None
        user = db.auth_user(username, password)
        if user is False or user is None:
            error = "Incorrect username/password."
            if not current_app.config["TESTING"]:
                time.sleep(2)  # slow down brute force attacks
        if error is None:
            session.clear()
            remember_me = request.form.get("remember")
            session.permanent = bool(remember_me)

            current_app.logger.debug("username " + username)
            session["username"] = username
            return redirect(url_for("index"))
        flash(error)

    return render_template("auth/login.html", config=current_app.config, user=g.user)


# edit user info, namely password
@bp.route("/edit", methods=("GET", "POST"))
def edit() -> ResponseReturnValue:
    if request.method == "POST":
        username = session["username"]
        old_pass = request.form["old_password"]
        password = generate_password_hash(request.form["password"])
        error: Optional[str] = None
        user = db.auth_user(username, old_pass)
        if user is False:
            error = "Bad old password."
        if error is None:
            if isinstance(user, dict):
                user["password"] = password
                db.save_user(user)
            flash("Success")
            return redirect(url_for("index"))
        flash(error)

    firmware_version = None
    system_repo_info = None
    if g.user and g.user.get("username") == "admin":
        firmware_version = db.get_firmware_version()
        system_repo_info = system_apps.get_system_repo_info(db.get_data_dir())
    return render_template(
        "auth/edit.html",
        user=g.user,
        firmware_version=firmware_version,
        system_repo_info=system_repo_info,
    )


@bp.before_app_request
def load_logged_in_user() -> None:
    username = session.get("username")
    if username is None:
        g.user = None
    else:
        g.user = db.get_user(username)  # will return none if non existant


@bp.route("/logout")
def logout() -> ResponseReturnValue:
    session.clear()
    flash("Logged Out")
    return redirect(url_for("auth.login"))


def login_required(view: Callable[..., Any]) -> Callable[..., Any]:
    @functools.wraps(view)
    def wrapped_view(**kwargs: Any) -> Any:
        if g.user is None:
            return redirect(url_for("auth.login"))
        return view(**kwargs)

    return wrapped_view


@bp.route("/set_theme_preference", methods=["POST"])
@login_required
def set_theme_preference() -> ResponseReturnValue:
    data = request.get_json()
    if not data or "theme" not in data:
        return {"status": "error", "message": "Missing theme data"}, 400

    theme = data["theme"]
    valid_themes = ["light", "dark", "system"]
    if theme not in valid_themes:
        return {"status": "error", "message": "Invalid theme value"}, 400

    # g.user is already the full user object from load_logged_in_user
    # and @login_required ensures g.user is populated.
    g.user["theme_preference"] = theme
    if db.save_user(g.user):
        # g.user is already updated in memory for the current request.
        current_app.logger.info(f"User {g.user['username']} set theme to {theme}")
        return {"status": "success", "message": "Theme preference updated"}
    else:
        current_app.logger.error(f"Failed to save theme for user {g.user['username']}")
        return {"status": "error", "message": "Failed to save theme preference"}, 500
