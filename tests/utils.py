import json
from pathlib import Path

config_path = Path("tests/users/testuser/testuser_debug.json")
uploads_path = Path("tests/users/testuser/apps")


def load_test_data(client):
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


def get_testuser_config_string():
    with config_path.open() as file:
        data = file.read()
        return data


def get_test_device_id():
    config = json.loads(get_testuser_config_string())
    return list(config["devices"].keys())[0]


def get_user_uploads_list():
    star_files = []
    for file in uploads_path.rglob("*.star"):
        relative_path = file.relative_to(uploads_path)
        star_files.append(str(relative_path))
    return star_files


def get_test_app_id():
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    return list(config["devices"][device_id]["apps"].keys())[0]


def get_test_app_dict():
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    return list(config["devices"][device_id]["apps"].values())[0]
