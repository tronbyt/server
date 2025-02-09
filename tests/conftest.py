import pytest
import os
from tronbyt_server import create_app, db

@pytest.fixture()
def app():
    app = create_app(test_config=True)

    with app.app_context():
        yield app

    # clean up / reset resources here
    print("delete testdb")
    os.system("rm users/testdb.sqlite")

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