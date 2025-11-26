import shutil
from io import BytesIO
from pathlib import Path

from fastapi.testclient import TestClient

from tronbyt_server import db
from tronbyt_server.utils import possibly_render
from sqlmodel import Session
from tests.conftest import get_test_session


def test_webp_upload_and_app_creation(auth_client: TestClient) -> None:
    # 1. Create a device
    response = auth_client.post(
        "/create",
        data={
            "name": "TESTDEVICE_WEBP",
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
        device = user.devices[device_id]

    # 2. Upload a .webp file
    webp_content = (
        b"RIFF\x0c\x00\x00\x00WEBPVP8 \x02\x00\x00\x00\x9d\x01*"  # Minimal valid WebP
    )
    files = {"file": ("test.webp", BytesIO(webp_content), "image/webp")}
    response = auth_client.post(
        f"/{device_id}/uploadapp", files=files, follow_redirects=False
    )
    assert response.status_code == 302
    assert response.headers["location"] == f"/{device_id}/addapp"

    # Check that the preview has been created
    preview_path = db.get_data_dir() / "apps" / "test.webp"
    assert preview_path.exists()
    assert preview_path.read_bytes() == webp_content

    # 3. Add the uploaded .webp app to the device
    response = auth_client.post(
        f"/{device_id}/addapp",
        data={"name": "test"},
        follow_redirects=False,
    )
    assert response.status_code == 302
    assert response.headers["location"] == "http://testserver/"

    # 4. Check that the app is added and file is copied
    with db.get_db() as db_conn:
        user = db.get_user(db_conn, "testuser")
        assert user
        device = user.devices[device_id]
        app = next((app for app in device.apps.values() if app.name == "test"), None)
        assert app is not None
        assert app.enabled is True
        assert Path(str(app.path)).suffix == ".webp"

        device_webp_path = (
            db.get_device_webp_dir(device_id) / f"{app.name}-{app.iname}.webp"
        )
        assert device_webp_path.exists()
        assert device_webp_path.read_bytes() == webp_content

        # 5. Check that possibly_render works correctly
        assert possibly_render(db_conn, user, device_id, app) is True

    # Cleanup
    preview_path.unlink()
    user_apps_path = db.get_users_dir() / user.username / "apps" / "test"
    if user_apps_path.exists():
        shutil.rmtree(user_apps_path)
