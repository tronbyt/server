"""Version information utilities for tronbyt-server."""

import json
import logging
from pathlib import Path

import requests
from packaging import version as pkg_version
from pydantic import BaseModel, ConfigDict, ValidationError, field_validator

logger = logging.getLogger(__name__)


class VersionInfo(BaseModel):
    """Typed model for version information."""

    version: str = "dev"
    commit_hash: str | None = None
    tag: str | None = None
    branch: str | None = None
    build_date: str | None = None

    # Ignore extra fields if present in the JSON file
    model_config = ConfigDict(extra="ignore")

    @field_validator("version")
    @classmethod
    def version_must_not_be_empty(cls, v: str) -> str:
        """Ensure version is not an empty string."""
        return v or "dev"


def get_version_info() -> VersionInfo:
    """Get version information from version.json file.

    Returns:
        VersionInfo instance populated from version.json or with defaults.
    """
    default_info = VersionInfo()

    try:
        # Look for version.json in the same directory as this module
        version_file = Path(__file__).parent / "version.json"

        if not version_file.exists():
            logger.warning(
                f"Version file not found at {version_file}, using default version '{default_info.version}'"
            )
            return default_info

        with version_file.open("r") as f:
            version_data = json.load(f)

        try:
            result = VersionInfo.model_validate(version_data)
        except ValidationError as e:
            logger.error(
                f"Version data validation failed: {e}, using default version '{default_info.version}'"
            )
            return default_info

        logger.debug(f"Loaded version info: {result.model_dump()}")
        return result

    except (json.JSONDecodeError, IOError, KeyError) as e:
        logger.warning(
            f"Failed to read version file: {e}, using default version '{default_info.version}'"
        )
        return default_info


def get_short_commit_hash() -> str | None:
    """Get the short commit hash (first 7 characters).

    Returns:
        Short commit hash string or None if not available.
    """
    commit_hash = get_version_info().commit_hash
    if commit_hash:
        return commit_hash[:7]
    return None


def check_for_updates(version_info: VersionInfo) -> tuple[bool, str | None]:
    """Check for updates on GitHub.

    Args:
        version_info: The current version info object.

    Returns:
        Tuple of (update_available, latest_release_url).
    """
    if not version_info.tag:
        return False, None

    try:
        response = requests.get(
            "https://api.github.com/repos/tronbyt/server/releases/latest",
            timeout=2.0,
        )
        response.raise_for_status()

        data = response.json()
        latest_tag = data.get("tag_name")
        html_url = data.get("html_url")

        if not latest_tag or not html_url:
            logger.warning("Incomplete update data from GitHub API.")
            return False, None

        # Remove 'v' prefix if present for comparison
        clean_latest = latest_tag.lstrip("v")
        clean_current = version_info.tag.lstrip("v")

        if pkg_version.parse(clean_latest) > pkg_version.parse(clean_current):
            return True, html_url

    except (
        requests.exceptions.RequestException,
        ValueError,
        pkg_version.InvalidVersion,
    ) as e:
        logger.warning(f"Failed to check for updates: {e}")

    return False, None
