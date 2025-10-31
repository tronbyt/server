"""One-time startup tasks for the application."""

import logging
import shutil
import sqlite3
from datetime import datetime
from pathlib import Path

from tronbyt_server import db, firmware_utils, system_apps
from tronbyt_server.config import get_settings


logger = logging.getLogger(__name__)


def backup_database(db_file: str) -> None:
    """Create a timestamped backup of the SQLite database."""
    db_path = Path(db_file)
    if not db_path.exists():
        logger.warning(f"Database file does not exist, skipping backup: {db_file}")
        return

    # Create backup directory if it doesn't exist
    backup_dir = db_path.parent / "backups"
    backup_dir.mkdir(exist_ok=True)

    # Get schema version from database
    schema_version = "unknown"
    try:
        with sqlite3.connect(db_file) as conn:
            cursor = conn.cursor()
            cursor.execute("SELECT schema_version FROM meta LIMIT 1")
            row = cursor.fetchone()
            if row:
                schema_version = str(row[0])
    except sqlite3.Error as e:
        logger.warning(f"Could not retrieve schema version: {e}")

    # Create timestamped backup filename with schema version
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    backup_filename = f"{db_path.stem}_{timestamp}_v{schema_version}.db"
    backup_path = backup_dir / backup_filename

    try:
        shutil.copy2(db_file, backup_path)
        logger.info(f"Database backed up to: {backup_path}")
    except (shutil.Error, OSError) as e:
        logger.error(f"Failed to backup database: {e}")


def run_once() -> None:
    """Run tasks that should only be executed once at application startup."""
    settings = get_settings()
    logger = logging.getLogger(__name__)
    logger.info("Running one-time startup tasks...")
    try:
        firmware_utils.update_firmware_binaries(db.get_data_dir())
    except Exception as e:
        logger.error(f"Failed to update firmware during startup: {e}")

    # Backup the database before initializing (only in production)
    # Skip system apps update in dev mode
    if settings.PRODUCTION == "1":
        system_apps.update_system_repo(db.get_data_dir())
        backup_database(settings.DB_FILE)
    else:
        logger.info("Skipping system apps update and database backup (dev mode)")

    try:
        with sqlite3.connect(settings.DB_FILE) as conn:
            db.vacuum(conn)
    except Exception as e:
        logger.warning(f"Could not vacuum database: {e}")

    logger.info("One-time startup tasks complete.")
