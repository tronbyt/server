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

    # Skip system apps update in dev mode
    if get_settings().PRODUCTION == "1":
        system_apps.update_system_repo(db.get_data_dir(), logger)
    else:
        logger.info("Skipping system apps update (dev mode)")

    # One-time fix for App recurrence fields (run once, then remove)
    try:
        db_connection = db.get_db()
        try:
            db.fix_app_recurrence_fields_one_time(db_connection, logger)
        finally:
            db_connection.close()
    except Exception as e:
        logger.error(f"Failed to run one-time recurrence fields fix: {e}")

    logger.info("One-time startup tasks complete.")
