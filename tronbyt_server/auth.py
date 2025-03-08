import functools
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


@bp.route("/register", methods=("GET", "POST"))
def register() -> ResponseReturnValue:
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
            user: User = {
                "username": username,
                "password": password,
                "email": email,
            }

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
            if current_app.config.get("PRODUCTION") == "1":
                session.permanent = True
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
