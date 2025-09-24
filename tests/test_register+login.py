from fastapi.testclient import TestClient

from tronbyt_server import db


def test_register_login_logout(auth_client: TestClient) -> None:
    with db.get_db() as db_conn:
        db.delete_user(db_conn, "testuser")
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
    assert response.headers["location"] == "/"

    response = auth_client.get("/auth/logout", follow_redirects=False)
    assert response.status_code == 302
    assert response.headers["location"] == "http://testserver/auth/login"


def test_unauth_index_with_users(client: TestClient) -> None:
    client.post("/auth/register_owner", data={"password": "adminpassword"})
    response = client.get("/")
    assert response.status_code == 200


def test_unauth_index_no_users(client: TestClient) -> None:
    response = client.get("/", follow_redirects=False)
    assert response.status_code in [302, 307]
    assert response.headers["location"].endswith("/auth/register_owner")
