import sqlite3

import pytest
from fastapi.testclient import TestClient

from tronbyt_server import db
from tronbyt_server.models import App, DeviceType
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


def _add_app_to_device(
    db_connection: sqlite3.Connection,
    user: User,
    device_id: str,
    *,
    iname: str = "test-app",
    name: str = "Test App",
    enabled: bool = True,
    uinterval: int = 10,
    display_time: int = 30,
    pushed: bool = False,
    last_render: int = 1234,
    empty_last_render: bool = False,
) -> App:
    device = user.devices[device_id]
    app = App(
        iname=iname,
        name=name,
        enabled=enabled,
        uinterval=uinterval,
        display_time=display_time,
        pushed=pushed,
        last_render=last_render,
        empty_last_render=empty_last_render,
    )
    device.apps[iname] = app
    user.devices[device_id] = device
    db.save_user(db_connection, user)
    return app


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

    def test_get_device_returns_extended_fields(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure device payload exposes new metadata fields."""
        device_id = list(device_user.devices.keys())[0]
        device = device_user.devices[device_id]

        app = _add_app_to_device(
            db_connection,
            device_user,
            device_id,
            iname="night-app",
            name="Night App",
            pushed=True,
            empty_last_render=True,
        )

        device.type = DeviceType.TRONBYT_S3
        device.notes = "Extra info"
        device.default_interval = 42
        device.night_mode_enabled = True
        device.night_mode_app = app.iname
        device.night_start = "21:30"
        device.night_end = "06:00"
        device.night_brightness = 64
        device.dim_time = "05:15"
        device.dim_brightness = 22
        device.pinned_app = app.iname
        device_user.devices[device_id] = device
        db.save_user(db_connection, device_user)

        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
        )
        assert response.status_code == 200
        payload = response.json()

        assert payload["type"] == "tronbyt_s3"
        assert payload["notes"] == "Extra info"
        assert payload["intervalSec"] == 42
        assert payload["pinnedApp"] == app.iname
        assert payload["nightMode"]["enabled"] is True
        assert payload["nightMode"]["app"] == app.iname
        assert payload["nightMode"]["startTime"] == "21:30"
        assert payload["nightMode"]["endTime"] == "06:00"
        assert payload["nightMode"]["brightness"] == 64
        assert payload["dimMode"]["startTime"] == "05:15"
        assert payload["dimMode"]["brightness"] == 22
        assert payload["autoDim"] is True

    def test_patch_device_updates_extended_fields(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure device updates support new fields and value validation."""
        device_id = list(device_user.devices.keys())[0]
        app = _add_app_to_device(
            db_connection, device_user, device_id, iname="evening", name="Evening App"
        )

        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={
                "intervalSec": 15,
                "nightModeEnabled": True,
                "nightModeApp": app.iname,
                "nightModeBrightness": 50,
                "nightModeStartTime": "2130",
                "nightModeEndTime": "6:45",
                "dimModeStartTime": "930",
                "dimModeBrightness": 30,
                "pinnedApp": app.iname,
            },
        )
        assert response.status_code == 200
        payload = response.json()

        assert payload["intervalSec"] == 15
        assert payload["nightMode"]["enabled"] is True
        assert payload["nightMode"]["app"] == app.iname
        assert payload["nightMode"]["startTime"] == "21:30"
        assert payload["nightMode"]["endTime"] == "06:45"
        assert payload["nightMode"]["brightness"] == 50
        assert payload["dimMode"]["startTime"] == "09:30"
        assert payload["dimMode"]["brightness"] == 30
        assert payload["pinnedApp"] == app.iname

    def test_patch_device_rejects_unknown_pinned_app(
        self,
        auth_client: TestClient,
        device_user: User,
    ) -> None:
        """Ensure pinnedApp validation rejects unknown installations."""
        device_id = list(device_user.devices.keys())[0]
        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={"pinnedApp": "missing-app"},
        )
        assert response.status_code == 400
        assert "Pinned app not found" in response.text

    def test_patch_device_clears_dim_mode(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Empty dimModeStartTime should clear stored dim time."""
        device_id = list(device_user.devices.keys())[0]
        device = device_user.devices[device_id]
        device.dim_time = "04:00"
        device.dim_brightness = 40
        device_user.devices[device_id] = device
        db.save_user(db_connection, device_user)

        response = auth_client.patch(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={"dimModeStartTime": ""},
        )
        assert response.status_code == 200
        payload = response.json()
        assert payload["dimMode"]["startTime"] is None


class TestDeviceInstallationsEndpoint:
    """Test cases for device installations endpoints."""

    def test_list_installations_returns_extended_fields(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure installation payload includes new metadata fields."""
        device_id = list(device_user.devices.keys())[0]
        app = _add_app_to_device(
            db_connection,
            device_user,
            device_id,
            iname="install-1",
            name="Sample App",
            enabled=False,
            pushed=True,
            uinterval=7,
            display_time=12,
            last_render=999,
            empty_last_render=True,
        )
        device = device_user.devices[device_id]
        device.pinned_app = app.iname
        device_user.devices[device_id] = device
        db.save_user(db_connection, device_user)

        response = auth_client.get(
            f"/v0/devices/{device_id}/installations",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
        )
        assert response.status_code == 200
        payload = response.json()
        assert "installations" in payload
        assert len(payload["installations"]) == 1

        installation = payload["installations"][0]
        assert installation["id"] == app.iname
        assert installation["appID"] == "Sample App"
        assert installation["enabled"] is False
        assert installation["pinned"] is True
        assert installation["pushed"] is True
        assert installation["renderIntervalMin"] == 7
        assert installation["displayTimeSec"] == 12
        assert installation["lastRenderAt"] == 999
        assert installation["isInactive"] is True

    def test_get_installation_returns_payload(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure single installation lookup exposes new fields."""
        device_id = list(device_user.devices.keys())[0]
        app = _add_app_to_device(
            db_connection,
            device_user,
            device_id,
            iname="install-2",
            name="Lookup App",
            uinterval=3,
            display_time=9,
        )
        device = device_user.devices[device_id]
        device.pinned_app = app.iname
        device_user.devices[device_id] = device
        db.save_user(db_connection, device_user)

        response = auth_client.get(
            f"/v0/devices/{device_id}/installations/{app.iname}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
        )
        assert response.status_code == 200
        payload = response.json()
        assert payload["id"] == app.iname
        assert payload["appID"] == "Lookup App"
        assert payload["enabled"] is True
        assert payload["pinned"] is True
        assert payload["renderIntervalMin"] == 3
        assert payload["displayTimeSec"] == 9

    def test_patch_installation_updates_fields(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure installation updates support new fields and return payload."""
        device_id = list(device_user.devices.keys())[0]
        app = _add_app_to_device(
            db_connection,
            device_user,
            device_id,
            iname="install-3",
            name="Update App",
        )

        response = auth_client.patch(
            f"/v0/devices/{device_id}/installations/{app.iname}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={
                "enabled": False,
                "pinned": True,
                "renderIntervalMin": 5,
                "displayTimeSec": 20,
            },
        )
        assert response.status_code == 200
        payload = response.json()

        assert payload["enabled"] is False
        assert payload["pinned"] is True
        assert payload["renderIntervalMin"] == 5
        assert payload["displayTimeSec"] == 20

        # Confirm persistence by fetching installation again
        refreshed = auth_client.get(
            f"/v0/devices/{device_id}/installations/{app.iname}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
        )
        assert refreshed.status_code == 200
        refreshed_payload = refreshed.json()
        assert refreshed_payload["pinned"] is True
        assert refreshed_payload["renderIntervalMin"] == 5

    def test_patch_installation_rejects_negative_values(
        self,
        auth_client: TestClient,
        device_user: User,
        db_connection: sqlite3.Connection,
    ) -> None:
        """Ensure validation rejects negative interval/time values."""
        device_id = list(device_user.devices.keys())[0]
        response_interval = auth_client.patch(
            f"/v0/devices/{device_id}/installations/unknown",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={"renderIntervalMin": -1},
        )
        assert response_interval.status_code == 404

        app = _add_app_to_device(
            db_connection,
            device_user,
            device_id,
            iname="install-4",
            name="Validation App",
        )

        response_negative = auth_client.patch(
            f"/v0/devices/{device_id}/installations/{app.iname}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={"renderIntervalMin": -5},
        )
        assert response_negative.status_code == 400
        assert "Render interval" in response_negative.text

        response_display = auth_client.patch(
            f"/v0/devices/{device_id}/installations/{app.iname}",
            headers={"Authorization": f"Bearer {device_user.api_key}"},
            json={"displayTimeSec": -3},
        )
        assert response_display.status_code == 400
        assert "Display time" in response_display.text
