import sqlite3
import time
from typing import Any, Callable, TypeVar

from tronbyt_server import db
from tronbyt_server.models.device import Device
from tronbyt_server.models.user import User

T = TypeVar("T")


def get_testuser(conn: sqlite3.Connection) -> User:
    user = db.get_user(conn, "testuser")
    if not user:
        raise Exception("testuser not found")
    return user


def get_user_by_username(conn: sqlite3.Connection, username: str) -> User | None:
    return db.get_user(conn, username)


def get_device_by_id(conn: sqlite3.Connection, device_id: str) -> Device | None:
    return db.get_device_by_id(conn, device_id)


def poll_for_change(
    func: Callable[[], T],
    expected_value: Any,
    timeout: float = 5.0,
    interval: float = 0.1,
) -> T:
    """
    Poll a function until its return value matches the expected value or a timeout is reached.

    Args:
        func: The function to poll.
        expected_value: The expected return value of the function.
        timeout: The maximum time to wait in seconds.
        interval: The time to wait between polls in seconds.

    Returns:
        The final return value of the function.

    Raises:
        TimeoutError: If the timeout is reached before the condition is met.
    """
    start_time = time.monotonic()
    result = None
    while time.monotonic() - start_time < timeout:
        result = func()
        if result == expected_value:
            return result
        time.sleep(interval)
    raise TimeoutError(
        f"Timeout reached while polling for value '{expected_value}'. Last value was '{result}'."
    )
