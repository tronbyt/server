"""Flash message utility."""

from typing import cast
from fastapi import Request


def flash(request: Request, message: str, category: str = "primary") -> None:
    """Store a message in the session to be displayed later."""
    if "_messages" not in request.session:
        request.session["_messages"] = []
    messages = cast(list[dict[str, str]], request.session["_messages"])
    messages.append({"message": message, "category": category})


def get_flashed_messages(request: Request) -> list[dict[str, str]]:
    """Retrieve and clear flashed messages from the session."""
    messages = request.session.pop("_messages", [])
    return cast(list[dict[str, str]], messages)
