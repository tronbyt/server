from fastapi.testclient import TestClient
from tests import utils
from tronbyt_server import db
from sqlmodel import Session
from tests.conftest import get_test_session


def test_app_create_edit_config_delete(auth_client: TestClient, db_connection) -> None:
    response = auth_client.post(
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
    assert response.status_code == 302
    user = utils.get_testuser()
    device_id = list(user.devices.keys())[0]

    r = auth_client.get(f"/{device_id}/addapp")
    assert r.status_code == 200

    r = auth_client.post(
        f"/{device_id}/addapp",
        data={
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "10",
            "notes": "",
        },
        follow_redirects=False,
    )
    assert r.status_code == 302

    user = utils.get_testuser()
    assert len(user.devices[device_id].apps) == 1

    device_id = list(user.devices.keys())[0]

    r = auth_client.get(f"/{device_id}/addapp")
    assert r.status_code == 200

    r = auth_client.post(
        f"/{device_id}/addapp",
        data={
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "10",
            "notes": "",
        },
    )
    assert r.status_code == 200

    user = utils.get_testuser()
    app_id = list(user.devices[device_id].apps.keys())[0]
    test_app = user.devices[device_id].apps[app_id]
    assert test_app.name == "NOAA Tides"

    r = auth_client.get(f"/{device_id}/{app_id}/configapp?delete_on_cancel=true")
    assert r.status_code == 200

    r = auth_client.post(
        f"/{device_id}/{app_id}/updateapp",
        data={
            "iname": app_id,
            "name": "NOAA Tides",
            "uinterval": "69",
            "display_time": "69",
            "notes": "69",
            "enabled": "true",
            "starttime": "",
            "endtime": "",
        },
        follow_redirects=False,
    )
    assert r.status_code == 302

    user = utils.get_testuser()
    test_app = user.devices[device_id].apps[app_id]

    assert test_app.uinterval == 69
    assert test_app.display_time == 69
    assert test_app.notes == "69"

    auth_client.post(f"/{device_id}/{app_id}/delete")

    user = utils.get_testuser()
    assert app_id not in user.devices[device_id].apps

    db.delete_device_dirs(device_id)
