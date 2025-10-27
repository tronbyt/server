"""Jinja2 templates configuration."""

import datetime as dt
import time

from pathlib import Path
from babel.dates import format_timedelta
from fastapi.templating import Jinja2Templates
from fastapi_babel import _

from tronbyt_server.config import get_settings
from tronbyt_server.flash import get_flashed_messages


def timeago(seconds: int) -> str:
    """Format a timestamp as a time ago string."""
    if seconds == 0:
        return str(_("Never"))
    return format_timedelta(
        dt.timedelta(seconds=seconds - int(time.time())),
        granularity="second",
        add_direction=True,
    )


templates = Jinja2Templates(directory=Path(__file__).parent.resolve() / "templates")
templates.env.globals["get_flashed_messages"] = get_flashed_messages
templates.env.globals["_"] = _
templates.env.globals["config"] = get_settings()
templates.env.filters["timeago"] = timeago
