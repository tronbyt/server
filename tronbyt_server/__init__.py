import os
from dotenv import load_dotenv

from flask import Flask
def create_app(test_config=None):
    load_dotenv()
    # create and configure the app
    app = Flask(__name__, instance_relative_config=True)
    if test_config is None:
        app.config.from_mapping(
        SECRET_KEY='lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh',
        MAX_CONTENT_LENGTH = 1000 * 1000, # 1mbyte upload size limit
        DOMAIN = os.environ['SERVER_HOSTNAME'] or 'localhost',
        MAIN_PORT = os.environ['SERVER_PORT'] or 8000,
        USERS_DIR = 'users',
    )
        
    else:
        app.config.from_mapping(
        SECRET_KEY='lksdj;as987q3908475ukjhfgklauy983475iuhdfkjghairutyh',
        MAX_CONTENT_LENGTH = 1000 * 1000, # 1mbyte upload size limit
        DOMAIN = os.environ['SERVER_HOSTANAME'] or 'localhost',
        USERS_DIR = 'tests/users',
    )

    try:
        os.makedirs(app.instance_path)
    except OSError:
        pass
    
    from . import auth
    app.register_blueprint(auth.bp)

    from . import manager
    app.register_blueprint(manager.bp)
    app.add_url_rule('/', endpoint='index')

    import time
    @app.template_filter('timeago')
    def timeago(seconds):
        if seconds == 0: return "Never"
        # caclulate the minutes between now and the seconds passed in
        secondsago = (time.time() - seconds)
        if seconds < 2:
            return " just now"
        elif secondsago < 60:
            return f"{int(secondsago)} seconds ago"
        elif secondsago < 3600:
            return f"{int(secondsago // 60)} minutes ago"
        elif secondsago >= 3600:
            return f"{int(secondsago // 3600)} hours ago"
    
    return app