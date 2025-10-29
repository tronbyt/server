import sqlite3
import pytest
from fastapi.testclient import TestClient
from starlette.websockets import WebSocketDisconnect

from tronbyt_server.models.user import User
from tests import utils


@pytest.fixture
def device_user_ws(auth_client: TestClient, db_connection: sqlite3.Connection) -> User:
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
    return utils.get_testuser(db_connection)


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

        # Explicitly close the websocket to ensure cleanup
        websocket.close()
