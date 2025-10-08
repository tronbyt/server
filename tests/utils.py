import sqlite3
from tronbyt_server import db
from tronbyt_server.models.user import User


def get_testuser(conn: sqlite3.Connection) -> User:
    user = db.get_user(conn, "testuser")
    if not user:
        raise Exception("testuser not found")
    return user


def get_user_by_username(conn: sqlite3.Connection, username: str) -> User | None:
    return db.get_user(conn, username)
