"""Jinja2 templates configuration."""

import datetime as dt
import time

from pathlib import Path
from babel.dates import format_timedelta
import jinja2
from fastapi.templating import Jinja2Templates
from fastapi_babel import _

from tronbyt_server.config import get_settings
from tronbyt_server.flash import get_flashed_messages
from tronbyt_server.dependencies import is_auto_login_active


def timeago(seconds: int, locale: str) -> str:
    """Format a timestamp as a time ago string."""
    if seconds == 0:
        return str(_("Never"))
    return format_timedelta(
        dt.timedelta(seconds=seconds - int(time.time())),
        granularity="second",
        add_direction=True,
        locale=locale,
    )


env = jinja2.Environment(
    loader=jinja2.FileSystemLoader(Path(__file__).parent.resolve() / "templates"),
    autoescape=True,
)
env.globals["get_flashed_messages"] = get_flashed_messages
env.globals["_"] = _
env.globals["config"] = get_settings()
env.globals["is_auto_login_active"] = is_auto_login_active
env.filters["timeago"] = timeago

templates = Jinja2Templates(env=env)
