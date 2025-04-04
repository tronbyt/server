from io import BytesIO

from flask.testing import FlaskClient

from . import utils


def test_upload_and_delete(client: FlaskClient) -> None:
    client.post("/auth/register", data={"username": "testuser", "password": "password"})
    client.post("/auth/login", data={"username": "testuser", "password": "password"})

    data = dict(
        file=(BytesIO(b"my file contents"), "report.star"),
    )
    # device is required to upload a file now.
    client.get("/create")
    client.post(
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
    client.post(f"/{dev_id}/uploadapp", content_type="multipart/form-data", data=data)

    assert "report/report.star" in utils.get_user_uploads_list()

    client.get(f"/{dev_id}/deleteupload/report.star")

    assert "report/report.star" not in utils.get_user_uploads_list()

    # test rejected bad extension
    data = dict(
        file=(BytesIO(b"my file contents"), "report.exe"),
    )

    client.post(f"/{dev_id}/uploadapp", content_type="multipart/form-data", data=data)
    assert "report.exe" not in utils.get_user_uploads_list()
