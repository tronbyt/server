import sqlite3
import pytest
from fastapi.testclient import TestClient

from tronbyt_server.models.user import User
from tests import utils


@pytest.fixture
def device_user(auth_client: TestClient, db_connection: sqlite3.Connection) -> User:
    """Fixture to create a user with a device."""
    response = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    assert response.status_code == 200
    return utils.get_testuser(db_connection)


class TestDevicesEndpoint:
    """Test cases for the /v0/devices endpoint"""

    def test_list_devices_success(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test successful listing of devices"""
        api_key = device_user.api_key
        response = auth_client.get(
            "/v0/devices", headers={"Authorization": f"Bearer {api_key}"}
        )
        assert response.status_code == 200
        data = response.json()
        assert "devices" in data
        assert len(data["devices"]) == 1
        assert data["devices"][0]["displayName"] == "TESTDEVICE"

    def test_list_devices_with_direct_auth_header(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test listing devices with direct Authorization header (no Bearer prefix)"""
        api_key = device_user.api_key
        response = auth_client.get("/v0/devices", headers={"Authorization": api_key})
        assert response.status_code == 200
        data = response.json()
        assert "devices" in data
        assert len(data["devices"]) == 1

    def test_list_devices_missing_auth_header(self, client: TestClient) -> None:
        """Test listing devices without Authorization header"""
        response = client.get("/v0/devices")
        assert response.status_code == 401
        assert "Invalid credentials" in response.text

    def test_list_devices_invalid_api_key(self, auth_client: TestClient) -> None:
        """Test listing devices with invalid API key"""
        response = auth_client.get(
            "/v0/devices", headers={"Authorization": "Bearer invalid_key"}
        )
        assert response.status_code == 401
        assert "Invalid credentials" in response.text

    def test_list_devices_empty_devices(
        self, auth_client: TestClient, db_connection: sqlite3.Connection
    ) -> None:
        """Test listing devices when user has no devices"""
        user = utils.get_testuser(db_connection)
        api_key = user.api_key
        response = auth_client.get(
            "/v0/devices", headers={"Authorization": f"Bearer {api_key}"}
        )

        assert response.status_code == 200
        data = response.json()
        assert "devices" in data
        assert len(data["devices"]) == 0


class TestDeviceEndpoint:
    """Test cases for the /v0/devices/<device_id> endpoint"""

    def test_get_device_with_user_api_key(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test successful retrieval of device info"""
        device_id = list(device_user.devices.keys())[0]
        api_key = device_user.api_key
        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {api_key}"},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["id"] == device_id
        assert data["displayName"] == "TESTDEVICE"

    def test_get_device_with_device_api_key(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device retrieval using device-specific API key"""
        device_id = list(device_user.devices.keys())[0]
        device_api_key = device_user.devices[device_id].api_key
        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_api_key}"},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["id"] == device_id

    def test_get_device_not_found(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Test device retrieval for non-existent device"""
        # Create a second user to get a different API key
        response = auth_client.post(
            "/auth/register",
            data={"username": "testuser2", "password": "password"},
            follow_redirects=False,
        )
        assert response.status_code == 302
        user2 = utils.get_user_by_username(db_connection, "testuser2")
        assert user2 is not None

        device_id = list(device_user.devices.keys())[0]
        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {user2.api_key}"},
        )
        assert response.status_code == 404

    def test_get_device_invalid_id_format(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device retrieval with invalid ID format"""
        api_key = device_user.api_key
        response = auth_client.get(
            "/v0/devices/invalid-id",
            headers={"Authorization": f"Bearer {api_key}"},
        )
        assert response.status_code == 422

    def test_get_device_unauthorized_api_key(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device retrieval with wrong API key"""
        device_id = list(device_user.devices.keys())[0]
        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": "Bearer wrong_key"},
        )
        assert response.status_code == 401

    def test_patch_device_with_user_api_key(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device update with user API key"""
        device_id = list(device_user.devices.keys())[0]
        api_key = device_user.api_key
        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {api_key}"},
            json={"brightness": 128, "autoDim": True},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["brightness"] == 128
        assert data["autoDim"] is True

    def test_patch_device_with_device_api_key(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device update with device-specific API key"""
        device_id = list(device_user.devices.keys())[0]
        device_api_key = device_user.devices[device_id].api_key
        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_api_key}"},
            json={"brightness": 64},
        )
        assert response.status_code == 200
        data = response.json()
        assert data["brightness"] == 64

    def test_patch_device_invalid_brightness(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device update with invalid brightness value"""
        device_id = list(device_user.devices.keys())[0]
        api_key = device_user.api_key
        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {api_key}"},
            json={"brightness": 300},
        )
        assert response.status_code == 400

    def test_patch_device_missing_auth(
        self, auth_client: TestClient, device_user: User
    ) -> None:
        """Test device update without authorization"""
        device_id = list(device_user.devices.keys())[0]
        response = auth_client.patch(
            f"/v0/devices/{device_id}", json={"brightness": 100}
        )
        assert response.status_code == 401
