import pytest
from fastapi.testclient import TestClient
from starlette.websockets import WebSocketDisconnect

from tronbyt_server.models.user import User
from tests import utils
from datetime import datetime
from typing import Any


@pytest.fixture
def device_user_ws(auth_client: TestClient) -> User:
    """Fixture to create a user with a device for websocket tests."""
    response = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE_WS",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
        follow_redirects=True,
    )
    assert response.status_code == 200
    return utils.get_testuser()


def test_websocket_invalid_device_id_format(auth_client: TestClient) -> None:
    """Test websocket connection with an invalid device ID format."""
    with pytest.raises(WebSocketDisconnect) as excinfo:
        with auth_client.websocket_connect("/invalid-id/ws"):
            pass
    assert excinfo.value.code == 1008


def test_websocket_nonexistent_device_id(auth_client: TestClient) -> None:
    """Test websocket connection with a non-existent but valid device ID."""
    with pytest.raises(WebSocketDisconnect) as excinfo:
        with auth_client.websocket_connect("/12345678/ws"):
            pass
    assert excinfo.value.code == 1008


def test_websocket_success_connection_and_data(
    auth_client: TestClient, device_user_ws: User
) -> None:
    """Test successful websocket connection and receiving data."""
    device_id = list(device_user_ws.devices.keys())[0]

    with auth_client.websocket_connect(f"/{device_id}/ws") as websocket:
        # It should send dwell time and brightness first
        data = websocket.receive_json()
        assert "dwell_secs" in data
        assert isinstance(data["dwell_secs"], int)
        data = websocket.receive_json()
        assert "brightness" in data
        assert isinstance(data["brightness"], int)

        # Then it should send the default image or an error message
        message = websocket.receive()
        if "bytes" in message:
            image_data = message["bytes"]
            assert image_data is not None
            assert len(image_data) > 0
        elif "text" in message:
            json_data = message["text"]
            assert "status" in json_data
            assert "message" in json_data


def test_websocket_client_messages(
    auth_client: TestClient, device_user_ws: User, db_connection
) -> None:
    """Test that the server correctly handles client messages."""
    device_id = list(device_user_ws.devices.keys())[0]

    with auth_client.websocket_connect(f"/{device_id}/ws") as websocket:
        # It should send dwell time and brightness first
        _ = websocket.receive_json()
        _ = websocket.receive_json()
        _ = websocket.receive()

        # The client can send "queued"
        websocket.send_json({"queued": 1})

        # After sending "queued", the protocol_version should be updated if it was None
        def get_protocol_version() -> int | None:
            device = utils.get_device_by_id(device_id)
            return device.info.protocol_version if device else None

        utils.poll_for_change(get_protocol_version, 1)
        device = utils.get_device_by_id(device_id)
        assert device is not None
        assert device.info.protocol_version == 1

        # The client can send "displaying"
        websocket.send_json({"displaying": 1})

        # The client can send client_info
        client_info: dict[str, Any] = {
            "client_info": {
                "firmware_version": "1.25.0",
                "firmware_type": "ESP32",
                "protocol_version": 1,
                "mac": "xx:xx:xx:xx:xx:xx",
            }
        }
        websocket.send_json(client_info)

        def check_full_client_info_update() -> bool:
            device = utils.get_device_by_id(device_id)
            if not device:
                return False
            return (
                device.last_seen is not None
                and device.info.firmware_version == "1.25.0"
                and device.info.firmware_type == "ESP32"
                and device.info.protocol_version == 1
                and device.info.mac_address == "xx:xx:xx:xx:xx:xx"
            )

        utils.poll_for_change(check_full_client_info_update, True)

        device = utils.get_device_by_id(device_id)
        assert device is not None
        assert isinstance(device.last_seen, datetime)
        assert device.info.firmware_version == "1.25.0"
        assert device.info.firmware_type == "ESP32"
        assert device.info.protocol_version == 1
        assert device.info.mac_address == "xx:xx:xx:xx:xx:xx"

        # The client can send partial client_info
        partial_client_info = {"client_info": {"firmware_version": "1.26.0"}}
        websocket.send_json(partial_client_info)

        def check_partial_client_info_update() -> bool:
            device = utils.get_device_by_id(device_id)
            if not device:
                return False
            return (
                device.info.firmware_version == "1.26.0"
                and device.info.firmware_type == "ESP32"
                and device.info.protocol_version == 1
                and device.info.mac_address == "xx:xx:xx:xx:xx:xx"
            )

        utils.poll_for_change(check_partial_client_info_update, True)

        device = utils.get_device_by_id(device_id)
        assert device is not None
        assert device.info.firmware_version == "1.26.0"
        assert device.info.firmware_type == "ESP32"
        assert device.info.protocol_version == 1
        assert device.info.mac_address == "xx:xx:xx:xx:xx:xx"
