import string

from fastapi.testclient import TestClient

from tronbyt_server import db_fastapi as db
from tronbyt_server.main import logger

from . import utils


class TestApiKeyGeneration:
    """Test cases for API key generation functionality"""

    def test_migrate_user_api_keys_generates_key_for_user_without_key(
        self, registered_client: TestClient
    ) -> None:
        """Test that migrate_user_api_keys generates API key for users without one"""
        # Create a user without API key by directly manipulating the user data
        user = db.get_user(logger, "testuser")
        assert user is not None

        # Remove the API key to simulate old user without API key
        if "api_key" in user:
            del user["api_key"]
        db.save_user(logger, user)

        # Verify user has no API key
        user_without_key = db.get_user(logger, "testuser")
        assert user_without_key is not None
        assert (
            "api_key" not in user_without_key
            or user_without_key.get("api_key") is None
        )

        # Run migration
        db.migrate_user_api_keys(logger)

        # Verify API key was generated
        migrated_user = db.get_user(logger, "testuser")
        assert migrated_user is not None
        assert "api_key" in migrated_user
        assert migrated_user["api_key"] is not None
        assert len(migrated_user["api_key"]) == 32

        # Verify API key contains only alphanumeric characters
        api_key = migrated_user["api_key"]
        assert all(c in string.ascii_letters + string.digits for c in api_key)

    def test_migrate_user_api_keys_preserves_existing_key(
        self, registered_client: TestClient
    ) -> None:
        """Test that migrate_user_api_keys doesn't overwrite existing API keys"""
        # Create a user (will automatically have API key)
        user = db.get_user(logger, "testuser")
        assert user is not None

        original_api_key = user.get("api_key")
        assert original_api_key is not None

        # Run migration
        db.migrate_user_api_keys(logger)

        # Verify API key wasn't changed
        user_after_migration = db.get_user(logger, "testuser")
        assert user_after_migration is not None
        assert user_after_migration["api_key"] == original_api_key

    def test_migrate_user_api_keys_handles_multiple_users(
        self, registered_client: TestClient
    ) -> None:
        """Test that migrate_user_api_keys handles multiple users correctly"""
        # Create multiple users
        registered_client.post(
            "/auth/register",
            data={"username": "user1", "password": "password"},
            cookies=registered_client.cookies,
        )
        registered_client.post(
            "/auth/register",
            data={"username": "user2", "password": "password"},
            cookies=registered_client.cookies,
        )

        # Remove API key from one user
        user1 = db.get_user(logger, "user1")
        assert user1 is not None
        if "api_key" in user1:
            del user1["api_key"]
        db.save_user(logger, user1)

        # Keep API key for the other user
        user2 = db.get_user(logger, "user2")
        assert user2 is not None
        original_key_user2 = user2.get("api_key")

        # Run migration
        db.migrate_user_api_keys(logger)

        # Verify first user got a new API key
        user1_after = db.get_user(logger, "user1")
        assert user1_after is not None
        assert "api_key" in user1_after
        assert user1_after["api_key"] is not None
        assert len(user1_after["api_key"]) == 32

        # Verify second user's API key is unchanged
        user2_after = db.get_user(logger, "user2")
        assert user2_after is not None
        assert user2_after["api_key"] == original_key_user2

    def test_migrate_user_api_keys_generates_valid_key(
        self, registered_client: TestClient
    ) -> None:
        """Test that migrate_user_api_keys generates a valid API key"""
        # Create a user and remove their API key to simulate old user
        user = db.get_user(logger, "testuser")
        assert user is not None

        # Remove the API key to simulate old user without API key
        if "api_key" in user:
            del user["api_key"]
        db.save_user(logger, user)

        # Run migration
        db.migrate_user_api_keys(logger)

        # Verify a valid API key was generated
        migrated_user = db.get_user(logger, "testuser")
        assert migrated_user is not None
        assert "api_key" in migrated_user
        api_key = migrated_user["api_key"]
        assert api_key is not None
        assert len(api_key) == 32

        # Verify API key contains only alphanumeric characters
        assert all(c in string.ascii_letters + string.digits for c in api_key)


class TestApiKeyRetrieval:
    """Test cases for API key retrieval functionality"""

    def test_get_user_by_api_key_success(self, registered_client: TestClient) -> None:
        """Test successful user retrieval by API key"""
        utils.load_test_data(registered_client)
        user = utils.get_testuser()
        api_key = user.api_key

        # Retrieve user by API key
        retrieved_user = db.get_user_by_api_key(logger, api_key)

        assert retrieved_user is not None
        assert retrieved_user["username"] == "testuser"
        assert retrieved_user["api_key"] == api_key

    def test_get_user_by_api_key_not_found(self, registered_client: TestClient) -> None:
        """Test user retrieval with non-existent API key"""
        utils.load_test_data(registered_client)

        # Try to retrieve user with invalid API key
        retrieved_user = db.get_user_by_api_key(logger, "nonexistent_key")

        assert retrieved_user is None

    def test_get_user_by_api_key_empty_string(self, registered_client: TestClient) -> None:
        """Test user retrieval with empty API key"""
        utils.load_test_data(registered_client)

        # Try to retrieve user with empty API key
        retrieved_user = db.get_user_by_api_key(logger, "")

        assert retrieved_user is None

    def test_get_user_by_api_key_multiple_users(
        self, registered_client: TestClient
    ) -> None:
        """Test user retrieval when multiple users exist"""
        # Create multiple users
        registered_client.post(
            "/auth/register",
            data={"username": "user1", "password": "password"},
            cookies=registered_client.cookies,
        )
        registered_client.post(
            "/auth/register",
            data={"username": "user2", "password": "password"},
            cookies=registered_client.cookies,
        )

        user1 = db.get_user(logger, "user1")
        user2 = db.get_user(logger, "user2")
        assert user1 is not None and user2 is not None

        api_key1 = user1["api_key"]
        api_key2 = user2["api_key"]

        # Ensure API keys are different
        assert api_key1 != api_key2

        # Test retrieval of first user
        retrieved_user1 = db.get_user_by_api_key(logger, api_key1)
        assert retrieved_user1 is not None
        assert retrieved_user1["username"] == "user1"

        # Test retrieval of second user
        retrieved_user2 = db.get_user_by_api_key(logger, api_key2)
        assert retrieved_user2 is not None
        assert retrieved_user2["username"] == "user2"

    def test_get_user_by_api_key_case_sensitive(
        self, registered_client: TestClient
    ) -> None:
        """Test that API key lookup is case sensitive"""
        utils.load_test_data(registered_client)
        user = utils.get_testuser()
        api_key = user.api_key

        # Try with different case (assuming API key has letters)
        if any(c.islower() for c in api_key):
            wrong_case_key = api_key.upper()
        else:
            wrong_case_key = api_key.lower()

        # Should not find user with wrong case
        retrieved_user = db.get_user_by_api_key(logger, wrong_case_key)
        assert retrieved_user is None

        # Should find user with correct case
        retrieved_user = db.get_user_by_api_key(logger, api_key)
        assert retrieved_user is not None


class TestApiKeyIntegration:
    """Integration tests for API key functionality"""

    def test_user_registration_creates_api_key(self, client: TestClient) -> None:
        """Test that user registration automatically creates an API key"""
        # Register owner
        client.post(
            "/auth/register_owner",
            data={"password": "adminpassword"},
            follow_redirects=False,
        )

        # Register a new user
        response = client.post(
            "/auth/register",
            data={"username": "newuser", "password": "password"},
            follow_redirects=False,
        )
        assert response.status_code == 303  # Redirect after successful registration
        assert response.headers["location"] == "http://testserver/auth/login"
        assert response.headers["location"] == "http://testserver/auth/login"

        # Verify user has API key
        user = db.get_user(logger, "newuser")
        assert user is not None
        assert "api_key" in user
        assert user["api_key"] is not None
        assert len(user["api_key"]) == 32

    def test_api_key_authentication_flow(self, registered_client: TestClient) -> None:
        """Test complete API key authentication flow"""
        # Create user and device
        device_id = utils.load_test_data(registered_client, follow_redirects=False)
        user = utils.get_testuser()
        api_key = user.api_key

        # Use API key to authenticate API request
        response = registered_client.get(
            "/v0/devices",
            headers={"Authorization": f"Bearer {api_key}"},
            follow_redirects=False,
        )

        assert response.status_code == 200

        # Verify the API returned correct device data
        import json

        data = json.loads(response.content)
        assert "devices" in data
        assert len(data["devices"]) == 1
        assert data["devices"][0]["id"] == device_id

    def test_api_key_uniqueness(self, client: TestClient) -> None:
        """Test that generated API keys are unique across users"""
        # Register owner
        client.post(
            "/auth/register_owner",
            data={"password": "adminpassword"},
            follow_redirects=False,
        )
        # Create multiple users
        api_keys = []
        for i in range(5):
            username = f"user{i}"
            client.post(
                "/auth/register",
                data={"username": username, "password": "password"},
                follow_redirects=False,
            )
            user = db.get_user(logger, username)
            assert user is not None
            api_keys.append(user["api_key"])

        # Verify all API keys are unique
        assert len(api_keys) == len(set(api_keys)), "API keys should be unique"
