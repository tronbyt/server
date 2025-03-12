import json
from pathlib import Path
from typing import List

from flask.testing import FlaskClient

from tronbyt_server.models.app import App

config_path = Path("tests/users/testuser/testuser_debug.json")
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


def get_testuser_config_string() -> str:
    with config_path.open() as file:
        data = file.read()
        return data


def get_test_device_id() -> str:
    config = json.loads(get_testuser_config_string())
    return str(list(config["devices"].keys())[0])


def get_user_uploads_list() -> List[str]:
    star_files = []
    for file in uploads_path.rglob("*.star"):
        relative_path = file.relative_to(uploads_path)
        star_files.append(str(relative_path))
    return star_files


def get_test_app_id() -> str:
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    return str(list(config["devices"][device_id]["apps"].keys())[0])


def get_test_app_dict() -> App:
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    app: App = list(config["devices"][device_id]["apps"].values())[0]
    return app
