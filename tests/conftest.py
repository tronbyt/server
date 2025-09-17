import pytest
from pathlib import Path
import shutil
from typing import Iterator

from fastapi.testclient import TestClient

from tronbyt_server.main import app as fastapi_app

@pytest.fixture()
def client() -> Iterator[TestClient]:
    # Reset the database before each test
    test_db_path = Path("tests/users/testdb.sqlite")
    if test_db_path.exists():
        test_db_path.unlink()

    users_dir = Path("tests/users")
    if users_dir.exists():
        shutil.rmtree(users_dir)
    users_dir.mkdir()

    with TestClient(fastapi_app) as client:
        yield client

@pytest.fixture()
def auth_client(client: TestClient) -> TestClient:
    # Create admin user
    client.post("/auth/register_owner", data={"password": "adminpassword"})

    # Register and login testuser
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})

    return client

@pytest.fixture()
def clean_app(client: TestClient) -> TestClient:
    # The client fixture already handles cleaning up the database and users directory
    return client
