import os
from tronbyt_server import db
from . import utils
# from unittest.mock import patch


# @patch("os.system")
# @patch("subprocess.Popen")
# def test_app_create_edit_config_delete(mock_os_system, mock_subprocess, client):
def test_app_create_edit_config_delete(client):
    # Configure the mock to return a successful result
    # mock_subprocess.return_value.returncode = 0
    # mock_os_system.return_value.returncode = 0

    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})
    client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "30",
        },
    )

    device_id = utils.get_test_device_id()

    r = client.get(f"/{device_id}/addapp")
    assert r.status_code == 200

    r = client.post(
        f"/{device_id}/addapp",
        data={
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "10",
            "notes": "",
        },
    )
    assert "NOAA Tides" in utils.get_testuser_config_string()
    app_id = utils.get_test_app_id()

    r = client.get(f"{device_id}/{app_id}/1/configapp")
    assert r.status_code == 200

    r = client.post(
        f"{device_id}/{app_id}/updateapp",
        data={
            "iname": app_id,
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "69",
            "notes": "69",
        },
    )
    print(r.data.decode())
    test_app_dict = utils.get_test_app_dict()
    print(str(test_app_dict))

    assert test_app_dict["uinterval"] == "69"
    assert test_app_dict["display_time"] == 69
    assert test_app_dict["notes"] == "69"

    client.get(f"{device_id}/{app_id}/delete")

    assert "TESTAPPUPDATED" not in utils.get_testuser_config_string()

    # delete the test device webp dir
    db.delete_device_dirs(device_id)
    assert not os.path.isdir(f"tronbyt_server/webp/{device_id}")
