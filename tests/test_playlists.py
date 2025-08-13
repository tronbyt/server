from flask.testing import FlaskClient

from tronbyt_server import db
from . import utils


def test_playlist_crud_operations(client: FlaskClient) -> None:
    """Test creating, reading, updating, and deleting playlists."""
    # Setup test data
    device_id = utils.load_test_data(client)

    # Test playlist list page (should be empty initially)
    r = client.get(f"/{device_id}/playlists")
    assert r.status_code == 200
    assert b"No Playlists Yet" in r.data

    # Test create playlist page
    r = client.get(f"/{device_id}/playlists/create")
    assert r.status_code == 200
    assert b"Create New Playlist" in r.data

    # Create a playlist
    r = client.post(
        f"/{device_id}/playlists/create",
        data={
            "name": "Test Playlist",
            "description": "A test playlist for unit testing",
        },
    )
    assert r.status_code == 302  # Redirect after creation

    # Verify playlist was created
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlists = db.get_device_playlists(device)
    assert len(playlists) == 1

    playlist_id = list(playlists.keys())[0]
    playlist = playlists[playlist_id]
    assert playlist["name"] == "Test Playlist"
    assert playlist["description"] == "A test playlist for unit testing"
    assert playlist["app_inames"] == []

    # Test playlist list page (should show the playlist now)
    r = client.get(f"/{device_id}/playlists")
    assert r.status_code == 200
    assert b"Test Playlist" in r.data
    assert b"No Playlists Yet" not in r.data

    # Test edit playlist page
    r = client.get(f"/{device_id}/playlists/{playlist_id}/edit")
    assert r.status_code == 200
    assert b"Edit Playlist" in r.data
    assert b"Test Playlist" in r.data

    # Update the playlist
    r = client.post(
        f"/{device_id}/playlists/{playlist_id}/edit",
        data={
            "name": "Updated Test Playlist",
            "description": "Updated description",
        },
    )
    assert r.status_code == 302  # Redirect after update

    # Verify playlist was updated
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlist = db.get_device_playlist(device, playlist_id)
    assert playlist is not None
    assert playlist["name"] == "Updated Test Playlist"
    assert playlist["description"] == "Updated description"

    # Delete the playlist
    r = client.post(f"/{device_id}/playlists/{playlist_id}/delete")
    assert r.status_code == 302  # Redirect after deletion

    # Verify playlist was deleted
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlists = db.get_device_playlists(device)
    assert len(playlists) == 0


def test_playlist_app_management(client: FlaskClient) -> None:
    """Test adding and removing apps from playlists."""
    # Setup test data with an app
    device_id = utils.load_test_data(client)

    # Add a test app first
    r = client.post(
        f"/{device_id}/addapp",
        data={
            "name": "clock",
            "uinterval": "10",
            "display_time": "5",
            "notes": "Test app",
        },
    )
    assert r.status_code == 302

    # Create a playlist
    r = client.post(
        f"/{device_id}/playlists/create",
        data={"name": "Test Playlist", "description": "Test playlist"},
    )
    assert r.status_code == 302

    # Get the playlist ID
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlist_id = list(db.get_device_playlists(device).keys())[0]

    # Get the app iname
    app_iname = list(device["apps"].keys())[0]

    # Test manage apps page
    r = client.get(f"/{device_id}/playlists/{playlist_id}/manage_apps")
    assert r.status_code == 200
    assert b"Manage Apps" in r.data
    assert b"Available Apps" in r.data

    # Add app to playlist
    r = client.post(
        f"/{device_id}/playlists/{playlist_id}/manage_apps",
        data={"action": "add", "app_iname": app_iname},
    )
    assert r.status_code == 302

    # Verify app was added to playlist
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlist = db.get_device_playlist(device, playlist_id)
    assert playlist is not None
    assert app_iname in playlist["app_inames"]

    # Remove app from playlist
    r = client.post(
        f"/{device_id}/playlists/{playlist_id}/manage_apps",
        data={"action": "remove", "app_iname": app_iname},
    )
    assert r.status_code == 302

    # Verify app was removed from playlist
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlist = db.get_device_playlist(device, playlist_id)
    assert app_iname not in playlist["app_inames"]


def test_playlist_validation(client: FlaskClient) -> None:
    """Test playlist validation and error handling."""
    # Setup test data
    device_id = utils.load_test_data(client)

    # Test creating playlist with empty name
    r = client.post(
        f"/{device_id}/playlists/create",
        data={"name": "", "description": "Test playlist"},
    )
    assert r.status_code == 302  # Redirects back with error

    # Verify no playlist was created
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlists = db.get_device_playlists(device)
    assert len(playlists) == 0

    # Test creating playlist with very long name
    long_name = "x" * 101  # Exceeds 100 character limit
    r = client.post(
        f"/{device_id}/playlists/create",
        data={"name": long_name, "description": "Test playlist"},
    )
    assert r.status_code == 302  # Redirects back with error

    # Verify no playlist was created
    user = utils.get_testuser()
    device = user["devices"][device_id]
    playlists = db.get_device_playlists(device)
    assert len(playlists) == 0


def test_playlist_database_functions(client: FlaskClient) -> None:
    """Test the database helper functions for playlists."""
    # Setup test data
    device_id = utils.load_test_data(client)
    user = utils.get_testuser()
    device = user["devices"][device_id]

    # Test creating playlist via database function
    playlist = db.create_playlist(
        device, "test123", "Test Playlist", "Test description"
    )
    assert playlist["id"] == "test123"
    assert playlist["name"] == "Test Playlist"
    assert playlist["description"] == "Test description"
    assert playlist["app_inames"] == []

    # Test getting playlist
    retrieved_playlist = db.get_device_playlist(device, "test123")
    assert retrieved_playlist is not None
    assert retrieved_playlist["name"] == "Test Playlist"

    # Test updating playlist
    success = db.update_playlist(device, "test123", name="Updated Name")
    assert success is True

    updated_playlist = db.get_device_playlist(device, "test123")
    assert updated_playlist["name"] == "Updated Name"

    # Test deleting playlist
    success = db.delete_playlist(device, "test123")
    assert success is True

    deleted_playlist = db.get_device_playlist(device, "test123")
    assert deleted_playlist is None
