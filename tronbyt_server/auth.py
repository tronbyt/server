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
from tronbyt_server.models.user import User

bp = Blueprint("auth", __name__, url_prefix="/auth")


def _generate_api_key() -> str:
    """Generate a random API key."""
    return "".join(
        secrets.choice(string.ascii_letters + string.digits) for _ in range(32)
    )


@bp.route("/register", methods=("GET", "POST"))
def register() -> ResponseReturnValue:
    # Check if max users limit is reached
    max_users = current_app.config.get("MAX_USERS", 100)  # Default to 0 (unlimited)
    if max_users > 0:
        users_count = len(db.get_all_users())
        if users_count >= max_users:
            flash("Maximum number of users reached. Registration is disabled.")
            return redirect(url_for("auth.login"))

    if not current_app.config["TESTING"]:
        time.sleep(2)
    # # only allow admin to register new users
    # if session['username'] != "admin":
    #     return redirect(url_for('manager.index'))
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
                flash(f"Registered as {username}.")
                return redirect(url_for("auth.login"))
            else:
                error = "Couldn't Save User"
        flash(error)
    return render_template("auth/register.html")


@bp.route("/login", methods=("GET", "POST"))
def login() -> ResponseReturnValue:
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
            if remember_me:
                session.permanent = True
            else:
                session.permanent = False

            current_app.logger.debug("username " + username)
            session["username"] = username
            return redirect(url_for("index"))
        flash(error)

    return render_template("auth/login.html")


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

    return render_template("auth/edit.html", user=g.user)


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

    user = db.get_user(g.user["username"])  # Fetch the full user object
    if not user:
        return {
            "status": "error",
            "message": "User not found",
        }, 404  # Should not happen if login_required works

    user["theme_preference"] = theme
    if db.save_user(user):
        # Update the theme in the session/g.user immediately if needed elsewhere in the same request,
        # though typically this is for subsequent requests.
        g.user["theme_preference"] = theme
        current_app.logger.info(f"User {g.user['username']} set theme to {theme}")
        return {"status": "success", "message": "Theme preference updated"}
    else:
        current_app.logger.error(f"Failed to save theme for user {g.user['username']}")
        return {"status": "error", "message": "Failed to save theme preference"}, 500
