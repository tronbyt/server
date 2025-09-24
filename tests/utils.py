from pathlib import Path
import sqlite3

import tronbyt_server.db as db
from tronbyt_server.config import settings
from tronbyt_server.models.user import User

uploads_path = Path("tests/users/testuser/apps")


def _get_db_conn() -> sqlite3.Connection:
    db_file = Path(settings.DB_FILE)
    db_file.parent.mkdir(exist_ok=True)
    conn = sqlite3.connect(db_file, check_same_thread=False)
    # conn.row_factory = sqlite3.Row
    return conn


def get_testuser() -> User:
    conn = _get_db_conn()
    user = db.get_user(conn, "testuser")
    conn.close()
    if not user:
        raise Exception("testuser not found")
    return user


def get_user_uploads_list() -> list[str]:
    star_files = []
    for file in uploads_path.rglob("*.star"):
        relative_path = file.relative_to(uploads_path)
        star_files.append(str(relative_path))
    return star_files
