"""Jinja2 templates configuration."""

import datetime as dt
import time

from pathlib import Path
from babel.dates import format_timedelta
from babel.numbers import get_decimal_symbol
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


def duration(td: dt.timedelta, locale: str) -> str:
    """Format a timedelta object as a human-readable duration with millisecond precision."""
    total_seconds = td.total_seconds()
    decimal_symbol = get_decimal_symbol(locale)

    if total_seconds < 60:  # Less than 1 minute, show in seconds with 3 decimal places
        return f"{total_seconds:.3f}".replace(".", decimal_symbol) + " s"
    elif total_seconds < 3600:  # Less than 1 hour, show minutes and seconds
        minutes = int(total_seconds // 60)
        seconds = total_seconds % 60
        return f"{minutes} m {seconds:.0f} s"
    else:  # 1 hour or more, use babel's format_timedelta for larger units
        return format_timedelta(
            td,
            granularity="second",
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
env.filters["duration"] = duration

templates = Jinja2Templates(env=env)
