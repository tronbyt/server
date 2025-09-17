from pathlib import Path
from typing import Iterator
import shutil

import pytest
from fastapi.testclient import TestClient

from tronbyt_server.main import app


@pytest.fixture()
def client() -> Iterator[TestClient]:
    # clean up / reset resources here
    test_db_path = Path("users/usersdb.sqlite")
    if test_db_path.exists():
        test_db_path.unlink()

    users_dir = Path("users")
    if users_dir.exists():
        shutil.rmtree(users_dir)
    users_dir.mkdir()

    with TestClient(app) as client:
        yield client


@pytest.fixture()
def registered_client(client: TestClient) -> TestClient:
    # Create admin user
    client.post("/auth/register_owner", data={"password": "adminpassword"})

    # Register testuser
    client.post(
        "/auth/register",
        data={"username": "testuser", "password": "password"},
        cookies=client.cookies,
    )

    return client
