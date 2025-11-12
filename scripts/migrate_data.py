import json
import logging
import sqlite3
from datetime import datetime

from dotenv import load_dotenv
from sqlalchemy.orm import sessionmaker
from sqlalchemy import create_engine

from tronbyt_server.models_sql import User, Device, App
from tronbyt_server.config import get_settings

load_dotenv()

logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)


def migrate_data() -> None:
    """
    Migrates data from the old json_data table to the new relational tables.
    """
    settings = get_settings()
    db_path = settings.DB_FILE
    logger.info(f"Attempting to connect to database at: {db_path}")
    engine = create_engine(f"sqlite:///{db_path}")
    SessionLocal = sessionmaker(autocommit=False, autoflush=False, bind=engine)
    db_session = SessionLocal()

    try:
        # Connect to the SQLite DB directly to read the old table
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("SELECT username, data FROM json_data")
        rows = cursor.fetchall()
        conn.close()

        if not rows:
            logger.info("No data found in json_data table. Nothing to migrate.")
            return

        logger.info(f"Found {len(rows)} users to migrate.")

        for username, data_json in rows:
            logger.info(f"Migrating user: {username}")
            user_data = json.loads(data_json)

            # Create User
            new_user = User(
                username=user_data.get("username"),
                password=user_data.get("password"),
                email=user_data.get("email", ""),
                api_key=user_data.get("api_key", ""),
                theme_preference=user_data.get("theme_preference", "system"),
                system_repo_url=user_data.get("system_repo_url", ""),
                app_repo_url=user_data.get("app_repo_url", ""),
            )
            db_session.add(new_user)
            db_session.flush()  # Flush to get the user ID for devices

            # Create Devices and Apps
            for device_id, device_data in user_data.get("devices", {}).items():
                last_seen_str = device_data.get("last_seen")
                last_seen_dt = (
                    datetime.fromisoformat(last_seen_str) if last_seen_str else None
                )

                new_device = Device(
                    id=device_id,
                    name=device_data.get("name", ""),
                    type=device_data.get("type", "tidbyt_gen1"),
                    api_key=device_data.get("api_key", ""),
                    img_url=device_data.get("img_url", ""),
                    ws_url=device_data.get("ws_url", ""),
                    notes=device_data.get("notes", ""),
                    brightness=device_data.get("brightness", 100),
                    night_mode_enabled=device_data.get("night_mode_enabled", False),
                    night_mode_app=device_data.get("night_mode_app", ""),
                    night_start=device_data.get("night_start"),
                    night_end=device_data.get("night_end"),
                    night_brightness=device_data.get("night_brightness", 0),
                    dim_time=device_data.get("dim_time"),
                    dim_brightness=device_data.get("dim_brightness"),
                    default_interval=device_data.get("default_interval", 15),
                    timezone=device_data.get("timezone"),
                    location=device_data.get("location"),
                    last_app_index=device_data.get("last_app_index", 0),
                    pinned_app=device_data.get("pinned_app"),
                    interstitial_enabled=device_data.get("interstitial_enabled", False),
                    interstitial_app=device_data.get("interstitial_app"),
                    last_seen=last_seen_dt,
                    info=device_data.get("info"),
                    user_id=new_user.id,
                )
                db_session.add(new_device)

                for iname, app_data in device_data.get("apps", {}).items():
                    new_app = App(
                        iname=iname,
                        name=app_data.get("name"),
                        uinterval=app_data.get("uinterval", 0),
                        display_time=app_data.get("display_time", 0),
                        notes=app_data.get("notes", ""),
                        enabled=app_data.get("enabled", True),
                        pushed=app_data.get("pushed", False),
                        order=app_data.get("order", 0),
                        last_render=app_data.get("last_render", 0),
                        path=app_data.get("path"),
                        start_time=app_data.get("start_time"),
                        end_time=app_data.get("end_time"),
                        days=app_data.get("days", []),
                        use_custom_recurrence=app_data.get(
                            "use_custom_recurrence", False
                        ),
                        recurrence_type=app_data.get("recurrence_type", "daily"),
                        recurrence_interval=app_data.get("recurrence_interval", 1),
                        recurrence_pattern=app_data.get("recurrence_pattern"),
                        recurrence_start_date=app_data.get("recurrence_start_date"),
                        recurrence_end_date=app_data.get("recurrence_end_date"),
                        config=app_data.get("config"),
                        empty_last_render=app_data.get("empty_last_render", False),
                        render_messages=app_data.get("render_messages", []),
                        autopin=app_data.get("autopin", False),
                        device_id=device_id,
                    )
                    db_session.add(new_app)

        db_session.commit()
        logger.info("Data migration completed successfully.")

    except Exception as e:
        logger.error(f"An error occurred during data migration: {e}")
        db_session.rollback()
    finally:
        db_session.close()


if __name__ == "__main__":
    migrate_data()
