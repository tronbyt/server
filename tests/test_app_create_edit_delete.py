from pathlib import Path

from tronbyt_server import db

from . import utils


def test_app_create_edit_delete(client):
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

    device_id = utils.get_test_device_id()
    r = client.post(
        f"/{device_id}/addapp",
        data={"name": "TESTAPP", "uinterval": "69", "display_time": "10", "notes": ""},
    )
    assert "TESTAPP" in utils.get_testuser_config_string()
    app_id = utils.get_test_app_id()
    r = client.post(
        f"{device_id}/{app_id}/updateapp",
        data={
            "iname": app_id,
            "name": "TESTAPPUPDATED",
            "uinterval": "69",
            "display_time": "69",
            "notes": "69",
        },
    )
    print(r.text)
    test_app_dict = utils.get_test_app_dict()
    print(str(test_app_dict))
    # assert test_app_dict['name'] == "TESTAPPUPDATED"
    assert test_app_dict["uinterval"] == "69"
    assert test_app_dict["display_time"] == 69
    assert test_app_dict["notes"] == "69"

    client.get(f"{device_id}/{app_id}/delete")

    assert "TESTAPPUPDATED" not in utils.get_testuser_config_string()

    # delete the test device webp dir
    db.delete_device_dirs(device_id)
    device_dir = Path("tronbyt_server") / "webp" / device_id
    assert not device_dir.is_dir()
