"""Version information utilities for tronbyt-server."""

import json
import logging
from pathlib import Path

logger = logging.getLogger(__name__)


def get_version_info() -> dict[str, str | None]:
    """Get version information from version.json file.

    Returns:
        dict with keys: 'version', 'commit_hash', 'tag', 'branch', 'build_date'
        If version file doesn't exist or can't be read, returns default values.
    """
    default_info = {
        "version": "dev",
        "commit_hash": None,
        "tag": None,
        "branch": None,
        "build_date": None,
    }

    try:
        # Look for version.json in the same directory as this module
        version_file = Path(__file__).parent / "version.json"

        if not version_file.exists():
            logger.debug(
                f"Version file not found at {version_file}, using default version 'dev'"
            )
            return default_info

        with version_file.open("r") as f:
            version_data = json.load(f)

        # Validate that we have the expected keys and return with defaults for missing ones
        result = default_info.copy()
        for key in result.keys():
            if key in version_data and version_data[key]:
                result[key] = version_data[key]

        logger.debug(f"Loaded version info: {result}")
        return result

    except (json.JSONDecodeError, IOError, KeyError) as e:
        logger.warning(f"Failed to read version file: {e}, using default version 'dev'")
        return default_info


def get_version() -> str:
    """Get just the version string.

    Returns:
        Version string, defaults to 'dev' if not available.
    """
    return get_version_info()["version"] or "dev"


def get_commit_hash() -> str | None:
    """Get the commit hash.

    Returns:
        Commit hash string or None if not available.
    """
    return get_version_info()["commit_hash"]


def get_short_commit_hash() -> str | None:
    """Get the short commit hash (first 7 characters).

    Returns:
        Short commit hash string or None if not available.
    """
    commit_hash = get_commit_hash()
    if commit_hash:
        return commit_hash[:7]
    return None
