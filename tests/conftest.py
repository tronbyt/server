from pathlib import Path

import pytest

from tronbyt_server import create_app


@pytest.fixture()
def app():
    app = create_app(test_config=True)

    with app.app_context():
        yield app

    # clean up / reset resources here
    print("delete testdb")
    test_db_path = Path("users/testdb.sqlite")
    if test_db_path.exists():
        test_db_path.unlink()


@pytest.fixture()
def client(app):
    return app.test_client()


@pytest.fixture()
def runner(app):
    return app.test_cli_runner()


@pytest.fixture()
def app_context(app):
    with app.app_context():
        yield
