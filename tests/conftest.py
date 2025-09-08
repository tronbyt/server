from pathlib import Path
from typing import Iterator

import pytest
from flask import Flask
from flask.ctx import AppContext
from flask.testing import FlaskClient, FlaskCliRunner

from tronbyt_server import create_app


@pytest.fixture()
def app() -> Iterator[Flask]:
    # Use test config with ENABLE_USER_REGISTRATION enabled by default for legacy compatibility
    test_config = {
        "ENABLE_USER_REGISTRATION": "1",  # Enable by default for tests
    }

    app = create_app(test_config)

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
def runner(app: Flask) -> FlaskCliRunner:
    return app.test_cli_runner()


@pytest.fixture()
def app_context(app: Flask) -> Iterator[AppContext]:
    with app.app_context() as ctx:
        yield ctx
