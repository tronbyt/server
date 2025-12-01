from unittest.mock import patch, MagicMock
import requests
from tronbyt_server import version
from tronbyt_server.version import VersionInfo


def test_check_for_updates_dev_version() -> None:
    """Test checking for updates with a dev version."""
    v_info = VersionInfo(version="dev")
    assert version.check_for_updates(v_info) == (False, None)


@patch("tronbyt_server.version.requests.get")
def test_check_for_updates_newer_available(mock_get: MagicMock) -> None:
    """Test checking for updates when a newer version is available."""
    mock_response = MagicMock()
    mock_response.status_code = 200
    mock_response.json.return_value = {
        "tag_name": "v1.0.1",
        "html_url": "http://example.com/v1.0.1",
    }
    mock_get.return_value = mock_response

    v_info = VersionInfo(version="1.0.0", tag="v1.0.0")
    update_available, url = version.check_for_updates(v_info)
    assert update_available is True
    assert url == "http://example.com/v1.0.1"


@patch("tronbyt_server.version.requests.get")
def test_check_for_updates_no_update(mock_get: MagicMock) -> None:
    """Test checking for updates when no newer version is available."""
    mock_response = MagicMock()
    mock_response.status_code = 200
    mock_response.json.return_value = {
        "tag_name": "v1.0.0",
        "html_url": "http://example.com/v1.0.0",
    }
    mock_get.return_value = mock_response

    v_info = VersionInfo(version="1.0.0", tag="v1.0.0")
    update_available, url = version.check_for_updates(v_info)
    assert update_available is False
    assert url is None


@patch("tronbyt_server.version.requests.get")
def test_check_for_updates_older_remote(mock_get: MagicMock) -> None:
    """Test checking for updates when remote version is older (should not happen usually but testing logic)."""
    mock_response = MagicMock()
    mock_response.status_code = 200
    mock_response.json.return_value = {
        "tag_name": "v0.9.9",
        "html_url": "http://example.com/v0.9.9",
    }
    mock_get.return_value = mock_response

    v_info = VersionInfo(version="1.0.0", tag="v1.0.0")
    update_available, url = version.check_for_updates(v_info)
    assert update_available is False
    assert url is None


@patch("tronbyt_server.version.requests.get")
def test_check_for_updates_error(mock_get: MagicMock) -> None:
    """Test checking for updates when an error occurs."""
    mock_get.side_effect = requests.exceptions.RequestException("Network error")

    v_info = VersionInfo(version="1.0.0", tag="v1.0.0")
    update_available, url = version.check_for_updates(v_info)
    assert update_available is False
    assert url is None


def test_check_for_updates_no_tag() -> None:
    """Test checking for updates when no tag is present."""
    v_info = VersionInfo(version="main-1234567", tag=None)
    assert version.check_for_updates(v_info) == (False, None)
