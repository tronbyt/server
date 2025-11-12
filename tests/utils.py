import time
from typing import Any, Callable, TypeVar

from sqlmodel import Session

from tronbyt_server import db
from tronbyt_server.models.device import Device
from tronbyt_server.models.user import User

T = TypeVar("T")


def get_testuser(session: Session) -> User:
    user = db.get_user(session, "testuser")
    if not user:
        raise Exception("testuser not found")
    return user


def get_user_by_username(session: Session, username: str) -> User | None:
    return db.get_user(session, username)


def get_device_by_id(session: Session, device_id: str) -> Device | None:
    return db.get_device_by_id(session, device_id)


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
    start_time = time.time()
    result = None
    while time.time() - start_time < timeout:
        result = func()
        if result == expected_value:
            return result
        time.sleep(interval)
    raise TimeoutError(
        f"Timeout reached while polling for value '{expected_value}'. Last value was '{result}'."
    )
