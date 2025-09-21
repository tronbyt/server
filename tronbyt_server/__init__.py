"""Tronbyt Server application factory and initialization."""

import datetime as dt
import os
import time
from pathlib import Path
from typing import Any, Dict, Optional

from babel.dates import format_timedelta
from dotenv import find_dotenv, load_dotenv
from flask import Flask, current_app, g, request
from flask_babel import Babel, _
from flask_sock import Sock
from werkzeug.serving import is_running_from_reloader

from tronbyt_server import db, firmware_utils, system_apps

babel = Babel()
sock = Sock()


def get_locale() -> Optional[str]:
    return request.accept_languages.best_match(current_app.config["LANGUAGES"])


def create_app(test_config: Optional[Dict[str, Any]] = None) -> Flask:
    if dotenv_path := find_dotenv(usecwd=True):
        print(f"Loading environment variables from {dotenv_path}")
        load_dotenv(dotenv_path)

    # create and configure the app
    app = Flask(__name__, instance_relative_config=True)
    if test_config is None:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_HOSTNAME=os.getenv("SERVER_HOSTNAME", "localhost"),
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="users",
            DATA_DIR=os.getenv("DATA_DIR", "data"),
            PRODUCTION=os.getenv("PRODUCTION", "1"),
            DB_FILE="users/usersdb.sqlite",
            LANGUAGES=["en", "de"],
            MAX_USERS=int(os.getenv("MAX_USERS", "100")),
            ENABLE_USER_REGISTRATION=os.getenv("ENABLE_USER_REGISTRATION", "0"),
        )
        if app.config.get("PRODUCTION") == "1":
            if app.config["SERVER_PROTOCOL"] == "https":
                app.config["SESSION_COOKIE_SECURE"] = True
            app.config["SESSION_COOKIE_SAMESITE"] = "Lax"
            app.logger.setLevel(os.getenv("LOG_LEVEL", "WARNING"))
        else:
            app.logger.setLevel(os.getenv("LOG_LEVEL", "INFO"))
    else:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            DB_FILE="tests/users/testdb.sqlite",
            LANGUAGES=["en"],
            SERVER_HOSTNAME="localhost",
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            USERS_DIR="tests/users",
            DATA_DIR=os.getenv("DATA_DIR", "data"),
            PRODUCTION="0",
            MAX_USERS=int(os.getenv("MAX_USERS", "100")),
            # ENABLE_USER_REGISTRATION enabled by default for test compatibility
            ENABLE_USER_REGISTRATION="1",
            TESTING=True,
        )
    babel.init_app(app, locale_selector=get_locale)

    instance_path = Path(app.instance_path)
    try:
        instance_path.mkdir(parents=True, exist_ok=True)
    except OSError:
        pass

    # Initialize the database within the application context
    with app.app_context():
        db.init_db()

        # The reloader will run this code twice, once in the main process and once in the child process.
        # This is a workaround to avoid running the update functions twice.
        if not is_running_from_reloader():
            # Update firmware before updating apps
            try:
                firmware_utils.update_firmware_binaries(db.get_data_dir(), app.logger)
            except Exception as e:
                app.logger.error(f"Failed to update firmware during startup: {e}")
            system_apps.update_system_repo(db.get_data_dir(), app.logger)

    from . import auth

    app.register_blueprint(auth.bp)

    from . import api

    app.register_blueprint(api.bp)

    from . import manager

    app.register_blueprint(manager.bp)
    app.add_url_rule("/", endpoint="index")

    sock.init_app(app)

    @app.template_filter("timeago")
    def timeago(seconds: int) -> str:
        if seconds == 0:
            return str(_("Never"))
        return format_timedelta(
            dt.timedelta(seconds=seconds - int(time.time())),
            granularity="second",
            add_direction=True,
            locale=get_locale(),
        )

    @app.teardown_appcontext
    def close_connection(exception: Any) -> None:
        db = getattr(g, "_database", None)
        if db is not None:
            db.close()

    return app
