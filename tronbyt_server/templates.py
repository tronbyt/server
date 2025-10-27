"""Jinja2 templates configuration."""

import datetime as dt
import time
from typing import Any

from babel.dates import format_timedelta
from fastapi.templating import Jinja2Templates
from fastapi_babel import _
from jinja2 import pass_context
from starlette.datastructures import URL

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


@pass_context
def custom_url_for(context: dict[str, Any], name: str, **path_params: Any) -> str:
    """
    Generate URLs using the SERVER_PROTOCOL from settings instead of request headers.

    This ensures URLs are generated with the correct protocol (http/https) based on
    the application configuration, rather than relying on proxy headers or the
    incoming request protocol.
    """
    settings = get_settings()
    request = context.get("request")

    if not request:
        # Fallback to relative URL if no request in context
        return f"/{name}"

    # Use request.url_for to build the URL (it handles routing)
    url = request.url_for(name, **path_params)

    # Replace the scheme with the configured protocol
    url_obj = URL(str(url))
    configured_url = url_obj.replace(scheme=settings.SERVER_PROTOCOL)

    return str(configured_url)


templates = Jinja2Templates(directory="tronbyt_server/templates")
templates.env.globals["get_flashed_messages"] = get_flashed_messages
templates.env.globals["_"] = _
templates.env.globals["config"] = get_settings()
templates.env.globals["url_for"] = custom_url_for
templates.env.filters["timeago"] = timeago
