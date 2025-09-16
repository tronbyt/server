from flask.testing import FlaskClient
from flask import Flask


def test_registration_disabled(app: Flask) -> None:
    # Disable open registration for this test
    app.config["ENABLE_USER_REGISTRATION"] = "0"

    # Create client after toggling config
    with app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        # Try to access registration page without being logged in
        response = client.get("/auth/register")
        assert response.status_code == 302  # Should redirect to login
        assert "/auth/login" in response.headers["Location"]

        # Check that the login page doesn't show registration link
        response = client.get("/auth/login")
        login_data = response.get_data(as_text=True)
        assert "Create User" not in login_data


def test_registration_enabled(clean_app: Flask) -> None:
    with clean_app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        with client.session_transaction() as sess:
            sess["username"] = "admin"
        response = client.get("/auth/register")
        assert response.status_code == 200
        assert "Register" in response.get_data(as_text=True)

        # Should be able to register a new user
        response = client.post(
            "/auth/register",
            data={"username": "newuser", "password": "password123"},
        )
        # Should redirect to register after successful registration
        assert response.status_code == 302
        assert "/auth/register" in response.headers["Location"]


def test_admin_can_always_register_users(app: Flask) -> None:
    """Admin users can access registration regardless of the env var."""
    # Prove exemption by disabling open registration
    app.config["ENABLE_USER_REGISTRATION"] = "0"

    with app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        # Simulate admin being logged in
        with client.session_transaction() as sess:
            sess["username"] = "admin"

        # Admin should be able to access registration page
        response = client.get("/auth/register")
        assert response.status_code == 200
        assert "Register" in response.get_data(as_text=True)


def test_max_users_limit_with_open_registration(app: Flask) -> None:
    # Ensure open registration and set a small limit (includes existing admin user)
    app.config["MAX_USERS"] = 2  # admin + 1 new user allowed

    with app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        with client.session_transaction() as sess:
            sess["username"] = "admin"
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
