from flask.testing import FlaskClient

from tronbyt_server import db

from . import utils




def test_app_create_edit_config_delete(auth_client: FlaskClient) -> None:
    auth_client.post(
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

    app_id = utils.get_test_app_id()
    assert utils.get_test_app_dict()["name"] == "NOAA Tides"

    r = auth_client.get(f"{device_id}/{app_id}/1/configapp")
    assert r.status_code == 200

    r = auth_client.post(
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

    assert test_app_dict["uinterval"] == 69
    assert test_app_dict["display_time"] == 69
    assert test_app_dict["notes"] == "69"

    auth_client.get(f"{device_id}/{app_id}/delete")

    user = utils.get_testuser()
    assert app_id not in user["devices"][device_id]["apps"]

    # delete the test device webp dir
    db.delete_device_dirs(device_id)
