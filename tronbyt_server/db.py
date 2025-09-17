import sqlite3
import json
from pathlib import Path
from tronbyt_server.config import get_settings

DATABASE_SCHEMA = """
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password TEXT NOT NULL,
    email TEXT,
    api_key TEXT,
    theme_preference TEXT,
    devices TEXT
);

CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER NOT NULL
);
"""

def get_db_path() -> Path:
    settings = get_settings()
    # In test mode, use a different database
    if settings.testing:
        db_file = "tests/users/testdb.sqlite"
    else:
        db_file = settings.db_file

    db_path = Path(db_file)
    db_path.parent.mkdir(parents=True, exist_ok=True)
    return db_path

def get_db():
    db_path = get_db_path()
    db = sqlite3.connect(db_path)
    db.row_factory = dict_factory
    try:
        yield db
    finally:
        db.close()

def init_db():
    db_path = get_db_path()
    db = sqlite3.connect(db_path)
    cursor = db.cursor()
    cursor.executescript(DATABASE_SCHEMA)
    # Check if schema_version is empty
    cursor.execute("SELECT version FROM schema_version")
    if cursor.fetchone() is None:
        cursor.execute("INSERT INTO schema_version (version) VALUES (1)")
    db.commit()
    db.close()

def dict_factory(cursor, row):
    d = {}
    for idx, col in enumerate(cursor.description):
        d[col[0]] = row[idx]
    return d
