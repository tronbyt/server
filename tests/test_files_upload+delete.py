from io import BytesIO

from fastapi.testclient import TestClient

from . import utils


def test_upload_and_delete(registered_client: TestClient) -> None:
    registered_client.post(
        "/auth/login",
        data={"username": "testuser", "password": "password"},
        follow_redirects=True,
    )
    data = {"file": ("report.star", BytesIO(b"my file contents"), "text/plain")}
    # device is required to upload a file now.
    registered_client.get("/create")
    registered_client.post(
        "/create",
        data={
            "name": "TESTDEVICE",
            "img_url": "TESTID",
            "api_key": "TESTKEY",
            "notes": "TESTNOTES",
            "brightness": "3",
        },
    )
    dev_id = utils.get_test_device_id()
    registered_client.post(
        f"/{dev_id}/uploadapp", files=data
    )

    assert "report/report.star" in utils.get_user_uploads_list()

    registered_client.get(
        f"/{dev_id}/deleteupload/report.star"
    )

    assert "report/report.star" not in utils.get_user_uploads_list()

    # test rejected bad extension
    data = {"file": ("report.exe", BytesIO(b"my file contents"), "text/plain")}

    registered_client.post(
        f"/{dev_id}/uploadapp", files=data
    )
    assert "report.exe" not in utils.get_user_uploads_list()
