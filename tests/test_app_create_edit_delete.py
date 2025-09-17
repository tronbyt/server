from fastapi.testclient import TestClient

from tronbyt_server import db_fastapi as db
from tronbyt_server.main import logger

from . import utils


def test_app_create_edit_config_delete(registered_client: TestClient) -> None:
    registered_client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=True,
    )
    create_response = registered_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
        follow_redirects=False,
    )
    assert create_response.status_code == 303

    device_id = utils.get_test_device_id()

    r = registered_client.get(f"/{device_id}/addapp", cookies=registered_client.cookies)
    assert r.status_code == 200

    r = registered_client.post(
        f"/{device_id}/addapp",
        data={
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "10",
            "notes": "",
        },
        cookies=registered_client.cookies,
    )

    app_id = utils.get_test_app_id()
    assert utils.get_test_app_dict().name == "NOAA Tides"

    r = registered_client.get(
        f"/{device_id}/{app_id}/1/configapp", cookies=registered_client.cookies
    )
    assert r.status_code == 200

    r = registered_client.post(
        f"/{device_id}/{app_id}/updateapp",
        data={
            "iname": app_id,
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "69",
            "notes": "69",
        },
        cookies=registered_client.cookies,
    )

    test_app_dict = utils.get_test_app_dict()

    assert test_app_dict.uinterval == 69
    assert test_app_dict.display_time == 69
    assert test_app_dict.notes == "69"

    registered_client.get(f"/{device_id}/{app_id}/delete", cookies=registered_client.cookies)

    user = utils.get_testuser()
    assert app_id not in user.devices[device_id].apps

    # delete the test device webp dir
    db.delete_device_dirs(logger, device_id)
