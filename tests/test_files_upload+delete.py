from io import BytesIO

from fastapi.testclient import TestClient

from tronbyt_server import db


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
