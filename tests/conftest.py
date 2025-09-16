from pathlib import Path
from typing import Iterator

import pytest
from flask import Flask
from flask.ctx import AppContext
from flask.testing import FlaskClient, FlaskCliRunner

from tronbyt_server import create_app


@pytest.fixture()
def app() -> Iterator[Flask]:
    app = create_app({"test_config": True})

    with app.app_context():
        yield app

    # clean up / reset resources here
    print("delete testdb")
    test_db_path = Path("tests/users/testdb.sqlite")
    if test_db_path.exists():
        test_db_path.unlink()


@pytest.fixture()
def client(app: Flask) -> FlaskClient:
    return app.test_client()


@pytest.fixture()
def auth_client(client: FlaskClient) -> FlaskClient:
    # Create admin user
    client.post("/auth/register_owner", data={"password": "adminpassword"})

    # Register and login testuser
    with client.session_transaction() as sess:
        sess["username"] = "admin"
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})

    return client


@pytest.fixture()
def runner(app: Flask) -> FlaskCliRunner:
    return app.test_cli_runner()


@pytest.fixture()
def app_context(app: Flask) -> Iterator[AppContext]:
    with app.app_context() as ctx:
        yield ctx


@pytest.fixture()
def clean_app() -> Iterator[Flask]:
    test_db_path = Path("tests/users/testdb.sqlite")
    if test_db_path.exists():
        test_db_path.unlink()
    app = create_app({"test_config": True})
    with app.app_context():
        yield app
    if test_db_path.exists():
        test_db_path.unlink()
