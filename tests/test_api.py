from pathlib import Path

from tronbyt_server import db

from . import utils


def test_api(client):
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
