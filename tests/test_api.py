import sqlite3
from operator import attrgetter
from pathlib import Path

from fastapi.testclient import TestClient

from . import utils
from tronbyt_server import db
from tronbyt_server.models.app import App


def test_api(auth_client: TestClient, db_connection: sqlite3.Connection) -> None:
    # Create a device
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
    assert response.headers["location"] == "/"

    # Get user to find device_id
    user = db.get_user(db_connection, "testuser")
    assert user
    device_id = list(user.devices.keys())[0]

    # Push base64 image via call to push
    data = """UklGRsYAAABXRUJQVlA4TLkAAAAvP8AHABcw/wKBJH/ZERYIJEHtr/b8B34K3DbbHievrd+SlSqA3btETOGfo881kEXFGJQRa+biGiCi/xPAXywwVqenXXoCj+L90gO4ryqALawrJOwGX1iVsGnVMRX8irHyqbzGagksXy0zsmlldlEbgotNM1Nfaw04UbmahSFTi0pgml3UgIvaNDNA4JMikAFTQ16YXYhDNk1jbiaGoTEgsnO5vqJ1KwpcpWXOiQrUoqbZyc3FIEb5PAA="""
    push_data = {"image": data}

    # Send the POST request
    url = f"/v0/devices/{device_id}/push"

    # Assert push fails with bad key
    response = auth_client.post(
        url,
        headers={"Authorization": "badkey", "Content-Type": "application/json"},
        json=push_data,
    )
    assert response.status_code == 401
    push_path = Path(db.get_device_webp_dir(device_id)) / "pushed"
    assert not push_path.exists()

    # Assert push succeeds with good key
    response = auth_client.post(
        url,
        headers={"Authorization": "TESTKEY", "Content-Type": "application/json"},
        json=push_data,
    )
    assert response.status_code == 200
    file_list = [
        f for f in push_path.iterdir() if f.is_file() and f.name.startswith("__")
    ]
    assert len(file_list) > 0

    # Call next and assert the pushed file is deleted
    auth_client.get(f"/{device_id}/next")
    file_list = [
        f for f in push_path.iterdir() if f.is_file() and f.name.startswith("__")
    ]
    assert len(file_list) == 0

    # Cleanup
    db.delete_device_dirs(device_id)
    assert not Path(f"tronbyt_server/webp/{device_id}").is_dir()


class TestMoveApp:
    def _setup_device_with_apps(
        self,
        auth_client: TestClient,
        db_connection: sqlite3.Connection,
        num_apps: int = 4,
    ) -> str:
        """Sets up a user, device, and apps for testing."""
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
        assert response.headers["location"] == "/"

        user = db.get_user(db_connection, "testuser")
        assert user
        device_id = list(user.devices.keys())[0]

        for i in range(1, num_apps + 1):
            response = auth_client.post(
                f"/{device_id}/addapp",
                data={
                    "name": "NOAA Tides",
                    "iname": f"app{i}",
                    "uinterval": "10",
                    "display_time": "10",
                },
                follow_redirects=False,
            )
            assert response.status_code == 302
        return device_id

    def _get_sorted_apps_from_db(
        self, db_connection: sqlite3.Connection, device_id: str
    ) -> list[App]:
        """Retrieves and sorts apps by order for a given device from the DB."""
        user = utils.get_testuser(db_connection)
        apps_dict = user.devices[device_id].apps
        apps_list = sorted(apps_dict.values(), key=attrgetter("order"))
        return apps_list

    def test_move_app_scenarios(
        self, auth_client: TestClient, db_connection: sqlite3.Connection
    ) -> None:
        device_id = self._setup_device_with_apps(auth_client, db_connection, 4)

        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        app1, app2, app3, app4 = (
            apps[0].iname,
            apps[1].iname,
            apps[2].iname,
            apps[3].iname,
        )

        # Move app2 down
        auth_client.post(f"/{device_id}/{app2}/moveapp", params={"direction": "down"})
        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        assert [app.iname for app in apps] == [app1, app3, app2, app4]
        for i, app in enumerate(apps):
            assert app.order == i

        # Move app2 up
        auth_client.post(f"/{device_id}/{app2}/moveapp", params={"direction": "up"})
        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        assert [app.iname for app in apps] == [app1, app2, app3, app4]
        for i, app in enumerate(apps):
            assert app.order == i

        # Move app1 up (should not change order)
        auth_client.post(f"/{device_id}/{app1}/moveapp", params={"direction": "up"})
        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        assert [app.iname for app in apps] == [app1, app2, app3, app4]

        # Move app4 down (should not change order)
        auth_client.post(f"/{device_id}/{app4}/moveapp", params={"direction": "down"})
        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        assert [app.iname for app in apps] == [app1, app2, app3, app4]

        # Move app1 down twice
        auth_client.post(f"/{device_id}/{app1}/moveapp", params={"direction": "down"})
        auth_client.post(f"/{device_id}/{app1}/moveapp", params={"direction": "down"})
        apps = self._get_sorted_apps_from_db(db_connection, device_id)
        assert [app.iname for app in apps] == [app2, app3, app1, app4]
        for i, app in enumerate(apps):
            assert app.order == i
