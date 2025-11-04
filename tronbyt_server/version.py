"""Version information utilities for tronbyt-server."""

import json
import logging
from pathlib import Path

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
