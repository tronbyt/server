from pathlib import Path
from typing import List

from fastapi.testclient import TestClient

from tronbyt_server import db
from tronbyt_server.models import App, Device, User

uploads_path = Path("tests/users/testuser/apps")


def load_test_data(client: TestClient) -> str:
    client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    return get_test_device_id()


def get_testuser() -> User:
    user_data = db.get_user(db.logger, "testuser")
    if not user_data:
        raise Exception("testuser not found")
    return User(**user_data)


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
