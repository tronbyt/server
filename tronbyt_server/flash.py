"""Flash message utility."""

from fastapi import Request


def flash(request: Request, message: str, category: str = "primary") -> None:
    """Store a message in the session to be displayed later."""
    if "_messages" not in request.session:
        request.session["_messages"] = []
    request.session["_messages"].append({"message": message, "category": category})


def get_flashed_messages(request: Request):
    """Retrieve and clear flashed messages from the session."""
    messages = request.session.pop("_messages", [])
    return messages
