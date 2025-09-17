from fastapi.testclient import TestClient

from . import utils


def test_device_operations(registered_client: TestClient) -> None:
    registered_client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=True,
    )
    r = registered_client.get("/create", follow_redirects=False)
    assert r.status_code == 200

    r = registered_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    assert utils.get_test_device().name == "TESTDEVICE"

    device_id = utils.get_test_device_id()
    # Test firmware generation page
    r = registered_client.get(f"/{device_id}/firmware", cookies=registered_client.cookies)
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
    r = registered_client.post(
        f"/{device_id}/firmware", data=data, cookies=registered_client.cookies
    )
    assert r.status_code == 200

    r = registered_client.post(
        f"/{device_id}/firmware", data=data, cookies=registered_client.cookies
    )
    assert r.status_code == 200
    assert r.headers["content-type"] == "application/octet-stream"
    assert (
        r.headers["content-disposition"]
        == f"attachment;filename=firmware_tidbyt_gen1_{device_id}.bin"
    )
    assert len(r.content) > 0

    # test /device_id/next works even when no app configured
    assert registered_client.get(f"/{device_id}/next").status_code == 200

    # Delete the device.
    r = registered_client.post(f"/{device_id}/delete", cookies=registered_client.cookies)
    testuser = utils.get_testuser()
    assert not testuser.devices
