"""One-time startup tasks for the application."""

import logging

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import get_settings


def run_once() -> None:
    """Run tasks that should only be executed once at application startup."""
    logging.basicConfig(level=get_settings().LOG_LEVEL)
    logger = logging.getLogger(__name__)
    logger.info("Running one-time startup tasks...")
    try:
        firmware_utils.update_firmware_binaries(db.get_data_dir(), logger)
    except Exception as e:
        logger.error(f"Failed to update firmware during startup: {e}")
    system_apps.update_system_repo(db.get_data_dir(), logger)
    logger.info("One-time startup tasks complete.")
