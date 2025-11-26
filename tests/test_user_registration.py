from fastapi.testclient import TestClient

from tronbyt_server.config import get_settings
from sqlmodel import Session
from tests.conftest import get_test_session

settings = get_settings()


def test_registration_disabled(auth_client: TestClient) -> None:
    settings.ENABLE_USER_REGISTRATION = "0"
    response = auth_client.get("/auth/register")
    assert response.status_code == 200


def test_registration_enabled(auth_client: TestClient) -> None:
    settings.ENABLE_USER_REGISTRATION = "1"
    response = auth_client.get("/auth/register")
    assert response.status_code == 200
    assert "Register" in response.text

    response = auth_client.post(
        "/auth/register",
        data={"username": "newuser", "password": "password123"},
    )
    assert response.status_code == 200


def test_max_users_limit_with_open_registration(auth_client: TestClient) -> None:
    settings.MAX_USERS = 2
    response = auth_client.post(
        "/auth/register",
        data={"username": "user1", "password": "password123"},
        follow_redirects=True,
    )
    assert response.status_code == 200
    response = auth_client.post(
        "/auth/register",
        data={"username": "user2", "password": "password123"},
        follow_redirects=True,
    )
    assert response.status_code == 200
    response = auth_client.post(
        "/auth/register",
        data={"username": "user3", "password": "password123"},
        follow_redirects=True,
    )
    assert response.status_code == 200
    assert "Maximum number of users reached" in response.text
