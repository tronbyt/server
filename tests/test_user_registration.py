import os
from fastapi.testclient import TestClient


def test_registration_disabled(client: TestClient) -> None:
    # Disable open registration for this test
    os.environ["ENABLE_USER_REGISTRATION"] = "0"

    client.post("/auth/register_owner", data={"password": "adminpassword"})
    # Try to access registration page without being logged in
    response = client.get("/auth/register", follow_redirects=False)
    assert response.status_code == 303  # Should redirect to login
    assert "login" in response.headers["location"]

    # Check that the login page doesn't show registration link
    response = client.get("/auth/login")
    login_data = response.text
    assert "Create User" not in login_data
    os.environ["ENABLE_USER_REGISTRATION"] = "1"


def test_registration_enabled(client: TestClient) -> None:
    os.environ["ENABLE_USER_REGISTRATION"] = "1"
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    response = client.get("/auth/register")
    assert response.status_code == 200
    assert "Register" in response.text

    # Should be able to register a new user
    response = client.post(
        "/auth/register",
        data={"username": "newuser", "password": "password123"},
        follow_redirects=False,
    )
    # Should redirect to login after successful registration
    assert response.status_code == 303
    assert "login" in response.headers["location"]


def test_admin_can_always_register_users(client: TestClient) -> None:
    """Admin users can access registration regardless of the env var."""
    # Prove exemption by disabling open registration
    os.environ["ENABLE_USER_REGISTRATION"] = "0"

    client.post("/auth/register_owner", data={"password": "adminpassword"})
    # Simulate admin being logged in
    client.post(
        "/auth/login", data={"username": "admin", "password": "adminpassword"}, follow_redirects=True
    )

    # Admin should be able to access registration page
    response = client.get("/auth/register")
    assert response.status_code == 200
    assert "Register" in response.text
    os.environ["ENABLE_USER_REGISTRATION"] = "1"


def test_max_users_limit_with_open_registration(client: TestClient) -> None:
    # Ensure open registration and set a small limit (includes existing admin user)
    os.environ["MAX_USERS"] = "2"  # admin + 1 new user allowed

    client.post("/auth/register_owner", data={"password": "adminpassword"})
    client.post(
        "/auth/login", data={"username": "admin", "password": "adminpassword"}, follow_redirects=True
    )
    # Register first user (should succeed)
    response = client.post(
        "/auth/register",
        data={"username": "user1", "password": "password123"},
        follow_redirects=False,
    )
    assert response.status_code == 303

    # Try to register second user (should fail due to limit)
    response = client.post(
        "/auth/register",
        data={"username": "user2", "password": "password123"},
        follow_redirects=False,
    )
    # Should redirect back to login with error message
    assert response.status_code == 303
    assert "login" in response.headers["location"]
    os.environ["MAX_USERS"] = "100"
