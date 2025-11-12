from pathlib import Path
from typing import Iterator

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
import shutil

from sqlmodel import Session

from tronbyt_server import db
from tronbyt_server.main import app as fastapi_app
from tronbyt_server.config import get_settings

settings = get_settings()


@pytest.fixture(scope="session")
def db_session(
    tmp_path_factory: pytest.TempPathFactory,
) -> Iterator[Session]:
    tmp_path = tmp_path_factory.mktemp("data")
    db_path = tmp_path / "testdb.sqlite"
    db_path.unlink(missing_ok=True)

    settings.DB_FILE = str(db_path)
    settings.DATA_DIR = str(tmp_path / "data")
    settings.USERS_DIR = str(tmp_path / "users")
    settings.ENABLE_USER_REGISTRATION = "1"

    with db.get_session() as session:
        yield session


@pytest.fixture(scope="session")
def app(db_session: Session) -> Iterator[FastAPI]:
    def get_session_override() -> Session:
        return db_session

    fastapi_app.dependency_overrides[db.get_session] = get_session_override

    yield fastapi_app

    fastapi_app.dependency_overrides.clear()


from sqlalchemy import text


@pytest.fixture(autouse=True)
def db_cleanup(db_session: Session) -> Iterator[None]:
    yield
    db_session.execute(text("SELECT name FROM sqlite_master WHERE type='table';"))
    tables = [
        row[0]
        for row in db_session.execute(
            text("SELECT name FROM sqlite_master WHERE type='table';")
        ).fetchall()
    ]
    for table in tables:
        if table != "sqlite_sequence":
            db_session.execute(text(f'DELETE FROM "{table}"'))
    db_session.commit()


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
