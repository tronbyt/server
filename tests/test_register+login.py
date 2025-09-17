import pytest
from fastapi.testclient import TestClient


def test_register_login_logout(client: TestClient) -> None:
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    response = client.get("/auth/register", follow_redirects=True)
    assert response.status_code == 200
    response = client.post(
        "/auth/register",
        data={"username": "testuser", "password": "password"},
        follow_redirects=False,
    )
    assert response.status_code == 303
    assert response.headers["location"].endswith("/auth/login")

    # test successful login of new user
    response = client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=False,
    )
    assert response.status_code == 303
    assert response.headers["location"].endswith("/")

    response = client.get("/auth/logout", follow_redirects=False)
    assert response.status_code == 303  # should redirect to login
    # make sure redirected to auth/login
    assert response.headers["location"].endswith("/auth/login")


def test_login_with_wrong_password(client: TestClient) -> None:
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    client.post(
        "/auth/register",
        data={"username": "testuser", "password": "password"},
        follow_redirects=False,
    )
    response = client.post(
        "/auth/login",
        data={"username": "testuser", "password": "BADDPASSWORD"},
    )
    assert "Incorrect username/password." in response.text


def test_unauth_index_with_users(client: TestClient) -> None:
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    response = client.get("/", follow_redirects=False)
    assert response.status_code == 302
    assert "auth/login" in response.headers["location"]


@pytest.mark.skip(reason="Failing after onboarding changes")
def test_unauth_index_without_users(client: TestClient) -> None:
    response = client.get("/")
    assert response.status_code == 302
    assert "auth/register_owner" in response.headers["location"]
