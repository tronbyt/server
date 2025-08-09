from typing import List, Required, TypedDict


class Playlist(TypedDict, total=False):
    id: Required[str]
    name: Required[str]
    description: str
    app_inames: List[str]  # List of app instance names in this playlist
    created_at: str
    updated_at: str
    order: int  # Display order in the playlist list


def validate_playlist_id(playlist_id: str) -> bool:
    """
    Validate playlist ID format.

    :param playlist_id: The playlist ID to validate.
    :return: True if valid, False otherwise.
    """
    if not playlist_id or not isinstance(playlist_id, str):
        return False

    # Allow alphanumeric characters, hyphens, and underscores
    # Length between 1 and 50 characters
    import re

    return bool(re.match(r"^[a-zA-Z0-9_-]{1,50}$", playlist_id))


def validate_playlist_name(name: str) -> bool:
    """
    Validate playlist name.

    :param name: The playlist name to validate.
    :return: True if valid, False otherwise.
    """
    if not name or not isinstance(name, str):
        return False

    # Allow reasonable length for display
    return 1 <= len(name.strip()) <= 100
