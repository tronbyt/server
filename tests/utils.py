from pathlib import Path
from typing import List

from flask.testing import FlaskClient

from tronbyt_server import db
from tronbyt_server.models.app import App
from tronbyt_server.models.device import Device
from tronbyt_server.models.user import User

uploads_path = Path("tests/users/testuser/apps")


def load_test_data(client: FlaskClient) -> str:
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})
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
    user = db.get_user("testuser")
    if not user:
        raise Exception("testuser not found")
    return user


def get_test_device_id() -> str:
    user = get_testuser()
    return list(user.get("devices", {}).keys())[0]


def get_test_device() -> Device:
    user = get_testuser()
    return list(user.get("devices", {}).values())[0]


def get_user_uploads_list() -> List[str]:
    star_files = []
    for file in uploads_path.rglob("*.star"):
        relative_path = file.relative_to(uploads_path)
        star_files.append(str(relative_path))
    return star_files


def get_test_app_id() -> str:
    user = get_testuser()
    device_id = get_test_device_id()
    return str(list(user["devices"][device_id]["apps"].keys())[0])


def get_test_app_dict() -> App:
    user = get_testuser()
    device_id = get_test_device_id()
    app: App = list(user["devices"][device_id]["apps"].values())[0]
    return app
