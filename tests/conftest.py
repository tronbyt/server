from pathlib import Path
from typing import Iterator, Generator
import sqlite3

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
import shutil
from sqlmodel import Session

from tronbyt_server import db, db_models
from tronbyt_server.main import app as fastapi_app
from tronbyt_server.dependencies import get_db
from tronbyt_server.config import get_settings

settings = get_settings()


@pytest.fixture(scope="session")
def db_connection(
    tmp_path_factory: pytest.TempPathFactory,
) -> Iterator[sqlite3.Connection]:
    from sqlmodel import create_engine as sqlmodel_create_engine

    tmp_path = tmp_path_factory.mktemp("data")
    db_path = tmp_path / "testdb.sqlite"
    db_path.unlink(missing_ok=True)

    settings.DB_FILE = str(db_path)
    settings.DATA_DIR = str(tmp_path / "data")
    settings.USERS_DIR = str(tmp_path / "users")
    settings.ENABLE_USER_REGISTRATION = "1"

    # Recreate the engine with the test database path
    original_engine = db_models.engine
    db_models.engine = sqlmodel_create_engine(
        f"sqlite:///{db_path}",
        echo=False,
        connect_args={"check_same_thread": False, "timeout": 10},
    )

    try:
        with sqlite3.connect(settings.DB_FILE, check_same_thread=False) as conn:
            # Initialize the database schema immediately after connection.
            db.init_db(conn)
            yield conn
    finally:
        # Restore original engine
        db_models.engine = original_engine


@pytest.fixture(scope="function")
def session(db_connection: sqlite3.Connection) -> Generator[Session, None, None]:
    """Provide a SQLModel session for tests."""
    with Session(db_models.engine) as session:
        yield session


@pytest.fixture(scope="session")
def app(db_connection: sqlite3.Connection) -> Iterator[FastAPI]:
    def get_db_override() -> Generator[Session, None, None]:
        with Session(db_models.engine) as session:
            yield session

    fastapi_app.dependency_overrides[get_db] = get_db_override

    yield fastapi_app

    fastapi_app.dependency_overrides.clear()


@pytest.fixture(autouse=True)
def db_cleanup(db_connection: sqlite3.Connection) -> Iterator[None]:
    # Clean up BEFORE test to ensure clean state
    cursor = db_connection.cursor()
    tables_in_order = [
        "apps",
        "locations",
        "devices",
        "users",
        "system_settings",
    ]
    for table in tables_in_order:
        try:
            cursor.execute(f'DELETE FROM "{table}"')
        except sqlite3.OperationalError:
            # Table doesn't exist yet, skip
            pass
    db_connection.commit()

    # Clear SQLModel session cache to ensure it sees the deletions
    with Session(db_models.engine) as session:
        session.expire_all()

    yield

    # Clean up AFTER test as well
    for table in tables_in_order:
        try:
            cursor.execute(f'DELETE FROM "{table}"')
        except sqlite3.OperationalError:
            pass
    db_connection.commit()

    # Clear SQLModel session cache again
    with Session(db_models.engine) as session:
        session.expire_all()


@pytest.fixture(autouse=True)
def settings_cleanup() -> Iterator[None]:
    original_enable_user_registration = settings.ENABLE_USER_REGISTRATION
    original_max_users = settings.MAX_USERS
    yield
    settings.ENABLE_USER_REGISTRATION = original_enable_user_registration
    settings.MAX_USERS = original_max_users


def get_test_session() -> Session:
    """Helper function for tests to get a database session."""
    return Session(db_models.engine)


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
