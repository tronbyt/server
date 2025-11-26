from pathlib import Path
from fastapi.testclient import TestClient
from tests import utils
from tronbyt_server.config import get_settings
from sqlmodel import Session
from tests.conftest import get_test_session


def test_device_operations(
    auth_client: TestClient, db_connection
) -> None:
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
        follow_redirects=False,
    )
    assert r.status_code == 302
    user = utils.get_testuser()
    device_id = list(user.devices.keys())[0]
    assert user.devices[device_id].name == "TESTDEVICE"

    r = auth_client.get(f"/{device_id}/firmware")
    assert r.status_code == 200

    # Create dummy firmware file for testing
    settings = get_settings()
    firmware_dir = Path(settings.DATA_DIR) / "firmware"
    firmware_dir.mkdir(parents=True, exist_ok=True)
    firmware_file = firmware_dir / "tidbyt-gen1.bin"
    content = (
        b"A" * 1000
        + b"XplaceholderWIFISSID____________\x00"
        + b"XplaceholderWIFIPASSWORD________________________________________\x00"
        + b"XplaceholderREMOTEURL___________________________________________________________________________________________________________\x00"
    )
    firmware_file.write_bytes(content)

    data = {
        "id": device_id,
        "img_url": f"http://m1Pro.local:8000/{device_id}/next",
        "wifi_ap": "Blah",
        "wifi_password": "Blah",
    }
    r = auth_client.post(f"/{device_id}/firmware", data=data)
    assert r.status_code == 200
    assert r.headers["content-type"] == "application/octet-stream"
    assert (
        r.headers["content-disposition"]
        == f"attachment;filename=firmware_tidbyt_gen1_{device_id}.bin"
    )
    assert len(r.content) > 0

    auth_client.post(
        f"/{device_id}/addapp",
        data={
            "name": "NOAA Tides",
            "iname": "noaa-tides",
            "uinterval": "10",
            "display_time": "10",
        },
        follow_redirects=False,
    )

    assert auth_client.get(f"/{device_id}/next").status_code == 200
