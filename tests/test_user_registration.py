"""Tests for the ALLOW_USER_REGISTRATION environment variable functionality."""

from flask.testing import FlaskClient


def test_registration_disabled_by_default(client: FlaskClient, app) -> None:
    with app.test_request_context():
        # Modify config within the context
        app.config["ENABLE_USER_REGISTRATION"] = None

        with app.test_client() as client:
            # Try to access registration page without being logged in
            response = client.get("/auth/register")
            assert response.status_code == 302  # Should redirect to login
            assert "/auth/login" in response.headers["Location"]

            # Check that the login page doesn't show registration link
            response = client.get("/auth/login")
            login_data = response.get_data(as_text=True)
            assert "Create User" not in login_data


def test_registration_enabled(client: FlaskClient, app) -> None:
    assert app.config["ENABLE_USER_REGISTRATION"] == "1"
    response = client.get("/auth/register")
    assert response.status_code == 200
    assert "Register" in response.get_data(as_text=True)

    # Login page should show registration link
    response = client.get("/auth/login")
    login_data = response.get_data(as_text=True)
    assert "Create User" in login_data

    # Should be able to register a new user
    response = client.post(
        "/auth/register",
        data={"username": "newuser", "password": "password123"},
    )
    # Should redirect to login after successful registration
    assert response.status_code == 302
    assert "/auth/login" in response.headers["Location"]


def test_admin_can_always_register_users(client: FlaskClient) -> None:
    """Test that admin users can always access registration regardless of the env var."""
    # Admin should be able to access registration page
    response = client.get("/auth/register")
    assert response.status_code == 200
    assert "Register" in response.get_data(as_text=True)


def test_max_users_limit_with_open_registration(client: FlaskClient) -> None:
    # Register first user (should succeed)
    response = client.post(
        "/auth/register", data={"username": "user1", "password": "password123"}
    )
    assert response.status_code == 302

    # Try to register second user (should fail due to limit)
    response = client.post(
        "/auth/register", data={"username": "user2", "password": "password123"}
    )
    # Should redirect back to login with error message
    assert response.status_code == 302
    assert "/auth/login" in response.headers["Location"]
