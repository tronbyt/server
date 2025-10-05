from pathlib import Path
from typing import Iterator
import sqlite3

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
import shutil

from tronbyt_server.main import app as fastapi_app
from tronbyt_server.dependencies import get_db
from tronbyt_server.config import settings


@pytest.fixture(scope="session")
def db_connection(
    tmp_path_factory: pytest.TempPathFactory,
) -> Iterator[sqlite3.Connection]:
    tmp_path = tmp_path_factory.mktemp("data")
    db_path = tmp_path / "testdb.sqlite"
    db_path.unlink(missing_ok=True)

    settings.DB_FILE = str(db_path)
    settings.DATA_DIR = str(tmp_path / "data")
    settings.USERS_DIR = str(tmp_path / "users")
    settings.ENABLE_USER_REGISTRATION = "1"

    with sqlite3.connect(settings.DB_FILE, check_same_thread=False) as conn:
        yield conn


@pytest.fixture(scope="session")
def app(db_connection: sqlite3.Connection) -> Iterator[FastAPI]:
    def get_db_override() -> sqlite3.Connection:
        return db_connection

    fastapi_app.dependency_overrides[get_db] = get_db_override

    yield fastapi_app

    fastapi_app.dependency_overrides.clear()


@pytest.fixture(autouse=True)
def db_cleanup(db_connection: sqlite3.Connection) -> Iterator[None]:
    yield
    cursor = db_connection.cursor()
    cursor.execute("SELECT name FROM sqlite_master WHERE type='table';")
    tables = [row[0] for row in cursor.fetchall()]
    for table in tables:
        if table != "sqlite_sequence":
            cursor.execute(f'DELETE FROM "{table}"')
    db_connection.commit()


@pytest.fixture(autouse=True)
def settings_cleanup() -> Iterator[None]:
    original_enable_user_registration = settings.ENABLE_USER_REGISTRATION
    original_max_users = settings.MAX_USERS
    yield
    settings.ENABLE_USER_REGISTRATION = original_enable_user_registration
    settings.MAX_USERS = original_max_users


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
