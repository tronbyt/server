from io import BytesIO
import os
import sqlite3

from fastapi.testclient import TestClient

from tronbyt_server import db
from tronbyt_server.config import settings


def test_upload_and_delete(auth_client: TestClient) -> None:
    files = {"file": ("report.star", BytesIO(b"my file contents"))}
    auth_client.get("/create")
    response = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
        follow_redirects=False,
    )
    assert response.status_code == 302

    with db.get_db() as db_conn:
        user = db.get_user(db_conn, "testuser")
        assert user
        device_id = list(user.devices.keys())[0]

    response = auth_client.post(
        f"/{device_id}/uploadapp", files=files, follow_redirects=False
    )
    assert response.status_code == 302
    assert response.headers["location"] == f"/{device_id}/addapp"

    response = auth_client.get(
        f"/{device_id}/deleteupload/report.star", follow_redirects=False
    )
    assert response.status_code == 302
    assert response.headers["location"] == f"/{device_id}/addapp"


def test_upload_bad_extension(
    auth_client: TestClient, db_connection: sqlite3.Connection
) -> None:
    files = {"file": ("report.exe", BytesIO(b"my file contents"))}
    auth_client.get("/create")
    response = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
        follow_redirects=False,
    )
    assert response.status_code == 302

    user = db.get_user(db_connection, "testuser")
    assert user
    device_id = list(user.devices.keys())[0]

    response = auth_client.post(
        f"/{device_id}/uploadapp", files=files, follow_redirects=False
    )
    assert response.status_code == 400
    assert "File type not allowed" in response.text

    user_apps_path = os.path.join(settings.USERS_DIR, user.username, "apps")
    assert not os.path.exists(os.path.join(user_apps_path, "report.exe"))
