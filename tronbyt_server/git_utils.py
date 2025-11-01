"""Git utilities using GitPython."""

import logging
from pathlib import Path

from git import InvalidGitRepositoryError, NoSuchPathError, Remote, Repo

logger = logging.getLogger(__name__)


def get_repo(path: Path) -> Repo | None:
    """Get a GitPython Repo object for the given path."""
    try:
        return Repo(path)
    except (InvalidGitRepositoryError, NoSuchPathError):
        return None


def get_primary_remote(repo: Repo) -> Remote | None:
    """Gets the 'origin' remote, or the first remote as a fallback."""
    if not repo.remotes:
        return None
    try:
        return repo.remotes.origin
    except IndexError:
        return repo.remotes[0]
