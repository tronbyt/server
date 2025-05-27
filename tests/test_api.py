from operator import itemgetter
from pathlib import Path

from flask.testing import FlaskClient

from tronbyt_server import db
from tronbyt_server.models.app import App

from . import utils


def test_api(client: FlaskClient) -> None:
    # load the test data (register,login,create device)
    device_id = utils.load_test_data(client)

    # push base64 image via call to push

    data = """UklGRsYAAABXRUJQVlA4TLkAAAAvP8AHABcw/wKBJH/ZERYIJEHtr/b8B34K3DbbHievrd+SlSqA3btETOGfo881kEXFGJQRa+biGiCi/xPAXywwVqenXXoCj+L90gO4ryqALawrJOwGX1iVsGnVMRX8irHyqbzGagksXy0zsmlldlEbgotNM1Nfaw04UbmahSFTi0pgml3UgIvaNDNA4JMikAFTQ16YXYhDNk1jbiaGoTEgsnO5vqJ1KwpcpWXOiQrUoqbZyc3FIEb5PAA="""

    # Create a JSON object with your data
    object = {
        "image": data,
        # "installationId": "test"
    }

    # Send the POST request using requests library
    url = f"/v0/devices/{device_id}/push"
    client.post(
        url,
        headers={"Authorization": "aa", "Content-Type": "application/json"},
        json=object,
    )
    # assert no exist because of bad key
    push_path = Path(db.get_device_webp_dir(device_id)) / "pushed"

    assert not push_path.exists()

    # good key
    client.post(
        url,
        headers={"Authorization": "TESTKEY", "Content-Type": "application/json"},
        json=object,
    )
    # assert a file starting with __ exist in the web device dir
    file_list = [
        f for f in push_path.iterdir() if f.is_file() and f.name.startswith("__")
    ]
    assert len(file_list) > 0

    # call next
    client.get(f"{device_id}/next")
    # assert the file is now deleted
    file_list = [
        f for f in push_path.iterdir() if f.is_file() and f.name.startswith("__")
    ]

    assert len(file_list) == 0

    # delete the test device webp dir
    db.delete_device_dirs(device_id)
    assert not Path(f"tronbyt_server/webp/{device_id}").is_dir()


class TestMoveApp:
    def _setup_device_with_apps(
        self, client: FlaskClient, num_apps: int = 4
    ) -> tuple[str, dict[str, App]]:
        """Sets up a user, device, and apps for testing."""
        device_id = utils.load_test_data(client)

        user = db.get_user("testuser")
        if not user:
            raise Exception("User not found after setup")

        apps = {}
        for i in range(num_apps):
            app_iname = f"app{i + 1}"
            apps[app_iname] = App(
                iname=app_iname,
                name=f"App {i + 1}",
                order=i,
                enabled=True,
                path=f"/fake/path/app{i + 1}.star",  # Required field
            )

        user_devices = user.get("devices", {})
        user_devices[device_id]["apps"] = apps
        user["devices"] = user_devices
        db.save_user(user)

        # Return device_id and the initial apps dict for reference
        return device_id, apps

    def _get_sorted_apps(self, device_id: str) -> list[App]:
        """Retrieves and sorts apps by order for a given device."""
        # Assuming g.user is set correctly by login_user or needs to be mocked
        # For tests, it's often better to fetch fresh data from db
        user = db.get_user("testuser")
        if not user or "devices" not in user or device_id not in user["devices"]:
            raise Exception("Device not found or user not set up correctly")

        apps_dict = user["devices"][device_id].get("apps", {})
        apps_list = sorted(apps_dict.values(), key=itemgetter("order"))
        return apps_list

    def test_move_app_scenarios(self, client: FlaskClient) -> None:
        device_id, _ = self._setup_device_with_apps(client, 4)

        # Initial state: app1 (0), app2 (1), app3 (2), app4 (3)

        # --- Test Move Down: Move app2 (order 1) down ---
        # assert False, f"device_id={device_id}, {utils.get_testuser()}"
        client.get(f"/{device_id}/app2/moveapp?direction=down")

        apps = self._get_sorted_apps(device_id)
        assert len(apps) == 4
        assert apps[0]["iname"] == "app1" and apps[0]["order"] == 0
        assert apps[1]["iname"] == "app3" and apps[1]["order"] == 1  # app3 moved up
        assert apps[2]["iname"] == "app2" and apps[2]["order"] == 2  # app2 moved down
        assert apps[3]["iname"] == "app4" and apps[3]["order"] == 3
        for i, app in enumerate(apps):
            assert app["order"] == i  # Check sequential order

        # Current state: app1 (0), app3 (1), app2 (2), app4 (3)

        # --- Test Move Up: Move app2 (order 2) up ---
        client.get(f"/{device_id}/app2/moveapp?direction=up")

        apps = self._get_sorted_apps(device_id)
        assert len(apps) == 4
        assert apps[0]["iname"] == "app1" and apps[0]["order"] == 0
        assert apps[1]["iname"] == "app2" and apps[1]["order"] == 1  # app2 moved up
        assert apps[2]["iname"] == "app3" and apps[2]["order"] == 2  # app3 moved down
        assert apps[3]["iname"] == "app4" and apps[3]["order"] == 3
        for i, app in enumerate(apps):
            assert app["order"] == i

        # Current state: app1 (0), app2 (1), app3 (2), app4 (3) - back to original

        # --- Edge Case: Move Top App (app1, order 0) Up ---
        client.get(f"/{device_id}/app1/moveapp?direction=up")
        apps = self._get_sorted_apps(device_id)
        assert apps[0]["iname"] == "app1" and apps[0]["order"] == 0
        assert apps[1]["iname"] == "app2" and apps[1]["order"] == 1
        assert apps[2]["iname"] == "app3" and apps[2]["order"] == 2
        assert apps[3]["iname"] == "app4" and apps[3]["order"] == 3
        for i, app in enumerate(apps):
            assert app["order"] == i

        # --- Edge Case: Move Bottom App (app4, order 3) Down ---
        client.get(f"/{device_id}/app4/moveapp?direction=down")
        apps = self._get_sorted_apps(device_id)
        assert apps[0]["iname"] == "app1" and apps[0]["order"] == 0
        assert apps[1]["iname"] == "app2" and apps[1]["order"] == 1
        assert apps[2]["iname"] == "app3" and apps[2]["order"] == 2
        assert apps[3]["iname"] == "app4" and apps[3]["order"] == 3
        for i, app in enumerate(apps):
            assert app["order"] == i

        # --- Scenario for previous bug potential: Multiple moves ---
        # Move app1 down twice
        client.get(f"/{device_id}/app1/moveapp?direction=down")  # app1 -> 1, app2 -> 0
        client.get(f"/{device_id}/app1/moveapp?direction=down")  # app1 -> 2, app3 -> 1
        # State: app2(0), app3(1), app1(2), app4(3)
        apps = self._get_sorted_apps(device_id)
        expected_order = {"app2": 0, "app3": 1, "app1": 2, "app4": 3}
        for app in apps:
            assert app["order"] == expected_order[app["iname"]]
        for i, app in enumerate(apps):
            assert app["order"] == i  # Final check for sequential and unique orders

        # Clean up (optional, depending on test runner setup)
        user = db.get_user("testuser")
        if user and "devices" in user and device_id in user["devices"]:
            del user["devices"][device_id]
            db.save_user(user)
        db.delete_user("testuser")  # If utils.register_user doesn't clean up
