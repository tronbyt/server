from flask.testing import FlaskClient

from . import utils


def test_device_operations(client: FlaskClient) -> None:
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})

    r = client.get("/create")
    assert r.status_code == 200

    r = client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    print(r.text)
    assert "TESTDEVICE" in utils.get_testuser_config_string()

    device_id = utils.get_test_device_id()
    # Test firmware generation page
    r = client.get(f"{device_id}/firmware")
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
    r = client.post(f"/{device_id}/firmware", data=data)
    assert r.status_code == 200

    r = client.post(f"/{device_id}/firmware", data=data)
    assert r.status_code == 200
    assert r.mimetype == "application/octet-stream"
    assert (
        r.headers["Content-Disposition"]
        == f"attachment;filename=firmware_tidbyt_gen1_{device_id}.bin"
    )
    assert len(r.data) > 0

    # test /device_id/next works even when no app configured
    assert client.get(f"{device_id}/next").status_code == 200

    # Delete the device.
    r = client.post(f"{device_id}/delete")
    assert "TESTDEVICE" not in utils.get_testuser_config_string()
