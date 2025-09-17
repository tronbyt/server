from flask import Flask
import pytest


def test_register_login_logout(clean_app: Flask) -> None:
    with clean_app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        with client.session_transaction() as sess:
            sess["username"] = "admin"
        response = client.get("/auth/register")
        assert response.status_code == 200
        response = client.post(
            "/auth/register", data={"username": "testuser", "password": "password"}
        )
        assert response.headers["Location"] == "/auth/register"

        # test successful login of new user
        response = client.post(
            "/auth/login", data={"username": "testuser", "password": "password"}
        )
        assert response.status_code == 302
        assert response.headers["Location"] == "/"

        response = client.get("/auth/logout")
        assert response.status_code == 302  # should redirect to login
        # make sure redirected to auth/login
        assert response.headers["Location"] == "/auth/login"


def test_login_with_wrong_password(clean_app: Flask) -> None:
    with clean_app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        with client.session_transaction() as sess:
            sess["username"] = "admin"
        client.post(
            "/auth/register", data={"username": "testuser", "password": "password"}
        )
        response = client.post(
            "/auth/login", data={"username": "testuser", "password": "BADDPASSWORD"}
        )
        assert "Incorrect username/password." in response.text


def test_unauth_index_with_users(clean_app: Flask) -> None:
    with clean_app.test_client() as client:
        client.post("/auth/register_owner", data={"password": "adminpassword"})
        response = client.get("/")
        assert response.status_code == 302
        assert "auth/login" in response.headers["Location"]


@pytest.mark.skip(reason="Failing after onboarding changes")
def test_unauth_index_without_users(clean_app: Flask) -> None:
    with clean_app.test_client() as client:
        response = client.get("/")
        assert response.status_code == 302
        assert "auth/register_owner" in response.headers["Location"]
