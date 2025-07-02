import json

from flask.testing import FlaskClient

from . import utils


class TestDevicesEndpoint:
    """Test cases for the /v0/devices endpoint"""

    def test_list_devices_success(self, client: FlaskClient) -> None:
        """Test successful listing of devices with valid API key"""
        # Setup test data
        device_id = utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        # Make request with user's API key
        response = client.get(
            "/v0/devices", headers={"Authorization": f"Bearer {api_key}"}
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert "devices" in data
        assert len(data["devices"]) == 1

        device_data = data["devices"][0]
        assert device_data["id"] == device_id
        assert device_data["displayName"] == "TESTDEVICE"
        assert "brightness" in device_data
        assert "autoDim" in device_data
        assert device_data["apiKey"] == "TESTKEY"

    def test_list_devices_with_direct_auth_header(self, client: FlaskClient) -> None:
        """Test listing devices with direct Authorization header (no Bearer prefix)"""
        utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.get("/v0/devices", headers={"Authorization": api_key})

        assert response.status_code == 200
        data = json.loads(response.data)
        assert "devices" in data
        assert len(data["devices"]) == 1

    def test_list_devices_missing_auth_header(self, client: FlaskClient) -> None:
        """Test listing devices without Authorization header"""
        response = client.get("/v0/devices")

        assert response.status_code == 400
        assert "Missing or invalid Authorization header" in response.data.decode()

    def test_list_devices_invalid_api_key(self, client: FlaskClient) -> None:
        """Test listing devices with invalid API key"""
        utils.load_test_data(client)

        response = client.get(
            "/v0/devices", headers={"Authorization": "Bearer invalid_key"}
        )

        assert response.status_code == 401
        assert "Invalid API key" in response.data.decode()

    def test_list_devices_empty_devices(self, client: FlaskClient) -> None:
        """Test listing devices when user has no devices"""
        # Register user but don't create any devices
        client.post(
            "/auth/register", data={"username": "testuser", "password": "password"}
        )
        client.post(
            "/auth/login", data={"username": "testuser", "password": "password"}
        )

        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.get(
            "/v0/devices", headers={"Authorization": f"Bearer {api_key}"}
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert "devices" in data
        assert len(data["devices"]) == 0


class TestDeviceEndpoint:
    """Test cases for the /v0/devices/<device_id> endpoint"""

    def test_get_device_with_user_api_key(self, client: FlaskClient) -> None:
        """Test successful retrieval of device info"""
        device_id = utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.get(
            f"/v0/devices/{device_id}", headers={"Authorization": f"Bearer {api_key}"}
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert data["id"] == device_id
        assert data["displayName"] == "TESTDEVICE"
        assert "brightness" in data
        assert "autoDim" in data

    def test_get_device_with_device_api_key(self, client: FlaskClient) -> None:
        """Test device retrieval using device-specific API key"""
        device_id = utils.load_test_data(client)
        device = utils.get_test_device()
        device_api_key = device["api_key"]

        response = client.get(
            f"/v0/devices/{device_id}", headers={"Authorization": device_api_key}
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert data["id"] == device_id

    def test_get_device_invalid_device_id(self, client: FlaskClient) -> None:
        """Test device retrieval with invalid device ID format"""
        utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.get(
            "/v0/devices/invalid-id-format",
            headers={"Authorization": f"Bearer {api_key}"},
        )

        assert response.status_code == 400
        assert "Invalid device ID" in response.data.decode()

    def test_get_device_nonexistent_device(self, client: FlaskClient) -> None:
        """Test device retrieval with nonexistent device ID"""
        utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.get(
            "/v0/devices/12345678", headers={"Authorization": f"Bearer {api_key}"}
        )

        assert response.status_code == 404

    def test_get_device_unauthorized_api_key(self, client: FlaskClient) -> None:
        """Test device retrieval with wrong API key"""
        device_id = utils.load_test_data(client)

        response = client.get(
            f"/v0/devices/{device_id}", headers={"Authorization": "Bearer wrong_key"}
        )

        assert response.status_code == 404

    def test_patch_device_with_user_api_key(self, client: FlaskClient) -> None:
        """Test device update with user API key"""
        device_id = utils.load_test_data(client)
        user = utils.get_testuser()
        api_key = user["api_key"]

        response = client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {api_key}"},
            json={"brightness": 128},
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert data["id"] == device_id
        assert data["brightness"] == 128

    def test_patch_device_with_device_api_key(self, client: FlaskClient) -> None:
        """Test device update with device-specific API key"""
        device_id = utils.load_test_data(client)
        device = utils.get_test_device()
        device_api_key = device["api_key"]

        response = client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": device_api_key},
            json={"brightness": 128},
        )

        assert response.status_code == 200
        data = json.loads(response.data)
        assert data["id"] == device_id
        assert data["brightness"] == 128

    def test_patch_device_missing_auth(self, client: FlaskClient) -> None:
        """Test device update without authorization"""
        device_id = utils.load_test_data(client)

        response = client.patch(f"/v0/devices/{device_id}", json={"brightness": 128})

        assert response.status_code == 400
        assert "Missing or invalid Authorization header" in response.data.decode()
