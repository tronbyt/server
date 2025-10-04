from pathlib import Path
from typing import Iterator
import sqlite3

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
import shutil

from tronbyt_server.main import app as fastapi_app
from tronbyt_server.db import init_db
from tronbyt_server.config import settings


@pytest.fixture()
def app(tmp_path: Path) -> Iterator[FastAPI]:
    # clean up / reset resources here
    db_path = tmp_path / "testdb.sqlite"
    db_path.unlink(missing_ok=True)

    settings.DB_FILE = str(db_path)
    settings.DATA_DIR = str(tmp_path / "data")
    settings.USERS_DIR = str(tmp_path / "users")
    settings.ENABLE_USER_REGISTRATION = "1"

    # Initialize the database
    conn = sqlite3.connect(settings.DB_FILE)
    with conn:
        init_db(conn)

    yield fastapi_app


@pytest.fixture()
def client(app: FastAPI) -> TestClient:
    return TestClient(app)


@pytest.fixture()
def auth_client(app: FastAPI) -> Iterator[TestClient]:
    with TestClient(app) as client:
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
        assert response.status_code == 302 or response.status_code == 409

        # Login as testuser
        response = client.post(
            "/auth/login",
            data={"username": "testuser", "password": "password"},
            follow_redirects=False,
        )
        assert response.status_code == 302
        yield client


@pytest.fixture()
def clean_app() -> Iterator[TestClient]:
    users_dir = Path("tests/users")
    if users_dir.exists():
        shutil.rmtree(users_dir)
    users_dir.mkdir()
    yield TestClient(fastapi_app)
    if users_dir.exists():
        shutil.rmtree(users_dir)
