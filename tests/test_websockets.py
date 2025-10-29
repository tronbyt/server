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
    import json

    device_id = list(device_user_ws.devices.keys())[0]

    with auth_client.websocket_connect(f"/{device_id}/ws") as websocket:
        # Collect initial frames: dwell_secs (required), optional brightness, then image or error
        dwell_seen = False
        image_or_error_seen = False

        # Read a limited number of frames to avoid hanging
        # The server sends: dwell_secs JSON, optional brightness JSON, then image bytes or error JSON
        for _ in range(5):
            try:
                message = websocket.receive()
            except Exception:
                # Connection closed or error - that's okay, we got what we needed
                break

            if "text" in message:
                try:
                    data = json.loads(message["text"])
                    if "dwell_secs" in data:
                        assert isinstance(data["dwell_secs"], int)
                        dwell_seen = True
                    elif "brightness" in data:
                        assert isinstance(data["brightness"], int)
                    elif "status" in data and "message" in data:
                        image_or_error_seen = True
                except (json.JSONDecodeError, KeyError):
                    pass
            elif "bytes" in message:
                image_data = message["bytes"]
                assert image_data is not None
                assert len(image_data) > 0
                image_or_error_seen = True

            # Stop once we have both required messages
            if dwell_seen and image_or_error_seen:
                break

        assert dwell_seen, "Expected dwell_secs JSON message"
        assert image_or_error_seen, "Expected image bytes or error JSON message"

        # Close the websocket - this should cause server-side tasks to cancel
        websocket.close()
