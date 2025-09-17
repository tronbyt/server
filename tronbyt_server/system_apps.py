import logging
from pathlib import Path
import git
from tronbyt_server.config import get_settings

logger = logging.getLogger(__name__)

def update_system_repo(data_dir: Path):
    settings = get_settings()
    apps_dir = data_dir / "apps"
    repo_url = settings.system_apps_repo

    if not repo_url:
        logger.info("SYSTEM_APPS_REPO not set, skipping system apps update.")
        return

    if apps_dir.exists():
        try:
            repo = git.Repo(apps_dir)
            origin = repo.remotes.origin
            origin.pull()
            logger.info("System apps repo updated.")
        except Exception as e:
            logger.error(f"Failed to update system apps repo: {e}")
    else:
        try:
            git.Repo.clone_from(repo_url, apps_dir)
            logger.info("System apps repo cloned.")
        except Exception as e:
            logger.error(f"Failed to clone system apps repo: {e}")

def update_firmware_binaries(data_dir: Path):
    # This is a placeholder for now.
    # The firmware files are already in the repository.
    # This function could be used to download new firmware versions in the future.
    logger.info("Skipping firmware binaries update (placeholder).")
