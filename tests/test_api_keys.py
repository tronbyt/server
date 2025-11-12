import pytest
from fastapi.testclient import TestClient
from tronbyt_server.models.user import User
import tronbyt_server.db as db
from . import utils
from sqlmodel import Session


class TestApiKeyGeneration:
    """Test cases for API key generation functionality"""


class TestApiKeyRetrieval:
    """Test cases for API key retrieval functionality"""

    def test_get_user_by_api_key_success(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test successful user retrieval by API key"""
        user = utils.get_testuser(session)
        api_key = user.api_key

        retrieved_user = db.get_user_by_api_key(session, api_key)
        assert retrieved_user is not None
        assert retrieved_user.username == "testuser"
        assert retrieved_user.api_key == api_key

    def test_get_user_by_api_key_not_found(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test user retrieval with non-existent API key"""
        retrieved_user = db.get_user_by_api_key(session, "nonexistent_key")
        assert retrieved_user is None

    def test_get_user_by_api_key_empty_string(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test user retrieval with empty API key"""
        retrieved_user = db.get_user_by_api_key(session, "")
        assert retrieved_user is None

    def test_get_user_by_api_key_multiple_users(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test user retrieval when multiple users exist"""
        auth_client.post(
            "/auth/register",
            data={"username": "user1", "password": "password"},
        )
        auth_client.post(
            "/auth/register",
            data={"username": "user2", "password": "password"},
        )

        user1 = db.get_user(session, "user1")
        user2 = db.get_user(session, "user2")
        assert user1 is not None and user2 is not None

        api_key1 = user1.api_key
        api_key2 = user2.api_key
        assert api_key1 != api_key2

        retrieved_user1 = db.get_user_by_api_key(session, api_key1)
        assert retrieved_user1 is not None
        assert retrieved_user1.username == "user1"

        retrieved_user2 = db.get_user_by_api_key(session, api_key2)
        assert retrieved_user2 is not None
        assert retrieved_user2.username == "user2"

    def test_get_user_by_api_key_case_sensitive(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test that API key lookup is case sensitive"""
        user = utils.get_testuser(session)
        api_key = user.api_key

        if any(c.islower() for c in api_key):
            wrong_case_key = api_key.upper()
        else:
            wrong_case_key = api_key.lower()

        retrieved_user = db.get_user_by_api_key(session, wrong_case_key)
        assert retrieved_user is None

        retrieved_user = db.get_user_by_api_key(session, api_key)
        assert retrieved_user is not None


@pytest.fixture
def api_key_user(auth_client: TestClient, db_session: Session) -> User:
    """Fixture to create a user with a device and an API key."""
    response = auth_client.post(
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
    assert response.status_code == 302
    return utils.get_testuser(session)


class TestApiKeyIntegration:
    """Integration tests for API key functionality"""

    def test_user_registration_creates_api_key(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test that user registration automatically creates an API key"""
        auth_client.post(
            "/auth/register",
            data={"username": "newuser", "password": "password"},
        )

        user = db.get_user(session, "newuser")
        assert user is not None
        assert user.api_key
        assert len(user.api_key) == 32

    def test_api_key_authentication_flow(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test complete API key authentication flow"""
        response = auth_client.post(
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
        assert response.status_code == 302
        user = utils.get_testuser(session)
        device_id = user.devices[0].id
        api_key = user.api_key

        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": f"Bearer {api_key}"},
        )
        assert response.status_code == 200
        data = response.json()
        assert "id" in data

        response = auth_client.get(
            f"/v0/devices/{device_id}",
            headers={"Authorization": "Bearer invalid_key"},
        )
        assert response.status_code == 401

        response = auth_client.get(f"/v0/devices/{device_id}")
        assert response.status_code == 401

    def test_api_key_uniqueness(
        self, auth_client: TestClient, db_session: Session
    ) -> None:
        """Test that generated API keys are unique across users"""
        api_keys: list[str] = []
        for i in range(5):
            username = f"user{i}"
            response = auth_client.post(
                "/auth/register",
                data={"username": username, "password": "password"},
                follow_redirects=False,
            )
            assert response.status_code == 302 or response.status_code == 409
            user = db.get_user(session, username)
            assert user is not None
            api_keys.append(user.api_key)

        assert len(api_keys) == len(set(api_keys)), "API keys should be unique"
