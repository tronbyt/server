from fastapi.testclient import TestClient

from tronbyt_server import db
from tests.conftest import get_test_session


def test_register_login_logout(auth_client: TestClient) -> None:
    session = get_test_session()
    try:
        db.delete_user(session, "testuser")
    finally:
        session.close()
    response = auth_client.get("/auth/register")
    assert response.status_code == 200
    response = auth_client.post(
        "/auth/register",
        data={"username": "testuser", "password": "password"},
    )
    assert response.status_code == 200

    response = auth_client.get("/auth/login")
    assert response.status_code == 200
    response = auth_client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=False,
    )
    assert response.status_code == 302
    assert response.headers["location"] == "http://testserver/"

    response = auth_client.get("/auth/logout", follow_redirects=False)
    assert response.status_code == 302
    assert response.headers["location"] == "http://testserver/auth/login"


def test_login_with_wrong_password(client: TestClient) -> None:
    # Create owner
    response = client.post(
        "/auth/register_owner",
        data={"password": "password"},
        follow_redirects=False,
    )
    assert response.status_code == 302
    # Register testuser
    response = client.post(
        "/auth/register",
        data={"username": "testuser", "password": "password"},
        follow_redirects=False,
    )
    assert response.status_code in [302, 409]

    # Login as testuser with bad password
    response = client.post(
        "/auth/login",
        data={"username": "testuser", "password": "BADDPASSWORD"},
        follow_redirects=False,
    )
    assert response.status_code == 401
    assert "Incorrect username/password." in response.text


def test_unauth_index_with_users(client: TestClient) -> None:
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    response = client.get("/", follow_redirects=False)
    assert response.status_code in [302, 307]
    assert response.headers["location"].endswith("/auth/login")


def test_unauth_index_no_users(client: TestClient) -> None:
    response = client.get("/", follow_redirects=False)
    assert response.status_code in [302, 307]
    assert response.headers["location"].endswith("/auth/register_owner")
