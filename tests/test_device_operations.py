from flask.testing import FlaskClient

from . import utils


def test_device_operations(auth_client: FlaskClient) -> None:
    r = auth_client.get("/create")
    assert r.status_code == 200

    r = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    assert utils.get_test_device()["name"] == "TESTDEVICE"

    device_id = utils.get_test_device_id()
    # Test firmware generation page
    r = auth_client.get(f"{device_id}/firmware")
    assert r.status_code == 200

    # id: device['id']
    # img_url: http://m1Pro.local:8000/9abe2858/next
    # wifi_ap: Blah
    # wifi_password: Blah
    data = {
        "id": device_id,
        "img_url": f"http://m1Pro.local:8000/{device_id}/next",
        "wifi_ap": "Blah",
        "wifi_password": "Blah",
    }
    r = auth_client.post(f"/{device_id}/firmware", data=data)
    assert r.status_code == 200

    r = auth_client.post(f"/{device_id}/firmware", data=data)
    assert r.status_code == 200
    assert r.mimetype == "application/octet-stream"
    assert (
        r.headers["Content-Disposition"]
        == f"attachment;filename=firmware_tidbyt_gen1_{device_id}.bin"
    )
    assert len(r.data) > 0

    # test /device_id/next works even when no app configured
    assert auth_client.get(f"{device_id}/next").status_code == 200

    # Delete the device.
    r = auth_client.post(f"{device_id}/delete")
    testuser = utils.get_testuser()
    assert not testuser.get("devices", {})
