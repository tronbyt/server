import pytest,os
from tronbyt_server import create_app

@pytest.fixture()
def app():
    app = create_app(test_config=True)
    app.config['TESTING'] = True

    yield app

    # clean up / reset resources here
    # remove the users/testuser directory
    os.system("rm -rf tests/users/testuser")



@pytest.fixture()
def client(app):
    return app.test_client()


@pytest.fixture()
def runner(app):
    return app.test_cli_runner()