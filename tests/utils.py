from pathlib import Path
from typing import List

from fastapi.testclient import TestClient

from tronbyt_server import db_fastapi as db
from tronbyt_server.main import logger
from tronbyt_server.models_fastapi import App, Device, User


uploads_path = Path("users/testuser/apps")


def load_test_data(client: TestClient, follow_redirects: bool = True) -> str:
    client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=follow_redirects,
    )
    client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
        follow_redirects=follow_redirects,
    )
    return get_test_device_id()


def get_testuser() -> User:
    user = db.get_user(logger, "testuser")
    if not user:
        raise Exception("testuser not found")
    return User(**user)


def get_test_device_id() -> str:
    user = get_testuser()
    return list(user.devices.keys())[0]


def get_test_device() -> Device:
    user = get_testuser()
    return list(user.devices.values())[0]


def get_user_uploads_list() -> List[str]:
    star_files = []
    for file in uploads_path.rglob("*.star"):
        relative_path = file.relative_to(uploads_path)
        star_files.append(str(relative_path))
    return star_files


def get_test_app_id() -> str:
    user = get_testuser()
    device_id = get_test_device_id()
    return str(list(user.devices[device_id].apps.keys())[0])


def get_test_app_dict() -> App:
    user = get_testuser()
    device_id = get_test_device_id()
    app: App = list(user.devices[device_id].apps.values())[0]
    return app
