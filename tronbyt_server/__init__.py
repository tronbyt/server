import os

from dotenv import load_dotenv
from flask import Flask

from tronbyt_server import db


def create_app(test_config=None):
    load_dotenv()
    # create and configure the app
    app = Flask(__name__, instance_relative_config=True)
    if test_config is None:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_HOSTNAME=os.getenv("SERVER_HOSTNAME", "localhost"),
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            PIXLET_RENDER_PORT1=os.getenv("PIXLET_RENDER_PORT1", "5100"),
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            PIXLET_API_URL=os.getenv("PIXLET_API_URL", ""),
            USERS_DIR="users",
            DB_FILE="usersdb.sqlite",
        )
    else:
        app.config.from_mapping(
            SECRET_KEY="lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh",
            MAX_CONTENT_LENGTH=1000 * 1000,  # 1mbyte upload size limit
            SERVER_PROTOCOL=os.getenv("SERVER_PROTOCOL", "http"),
            PIXLET_RENDER_PORT1=os.getenv("PIXLET_RENDER_PORT1", "5100"),
            DB_FILE="testdb.sqlite",
            SERVER_HOSTNAME="localhost",
            MAIN_PORT=os.getenv("SERVER_PORT", "8000"),
            PIXLET_API_URL=os.getenv("PIXLET_API_URL", ""),
            USERS_DIR="tests/users",
            PRODUCTION=0,
            TESTING=True,
        )

    try:
        os.makedirs(app.instance_path)
    except OSError:
        pass

    # Initialize the database within the application context
    with app.app_context():
        db.init_db()

    from . import auth

    app.register_blueprint(auth.bp)

    from . import api

    app.register_blueprint(api.bp)
    # app.add_url_rule("/api", endpoint="api")

    from . import manager

    app.register_blueprint(manager.bp)
    app.add_url_rule("/", endpoint="index")

    import time

    @app.template_filter("timeago")
    def timeago(seconds):
        if seconds == 0:
            return "Never"
        # caclulate the minutes between now and the seconds passed in
        secondsago = time.time() - seconds
        if seconds < 2:
            return " just now"
        elif secondsago < 60:
            return f"{int(secondsago)} seconds ago"
        elif secondsago < 3600:
            return f"{int(secondsago // 60)} minutes ago"
        elif secondsago >= 3600:
            return f"{int(secondsago // 3600)} hours ago"

    return app
