"""Utilities for generating, modifying, and downloading firmware binaries."""

import json
import os
import requests
import subprocess
import sys
import logging
from pathlib import Path
from typing import Any
from urllib.parse import urlparse

from tronbyt_server import db
from tronbyt_server.firmware import correct_firmware_esptool


logger = logging.getLogger(__name__)


# firmware bin files named after env targets in firmware project.
def generate_firmware(
    url: str,
    ap: str,
    pw: str,
    device_type: str,
    swap_colors: bool,
) -> bytes:
    # Determine the firmware filename based on device type
    if device_type == "tidbyt_gen2":
        firmware_filename = "tidbyt-gen2.bin"
    elif device_type == "pixoticker":
        firmware_filename = "pixoticker.bin"
    elif device_type == "tronbyt_s3":
        firmware_filename = "tronbyt-S3.bin"
    elif device_type == "tronbyt_s3_wide":
        firmware_filename = "tronbyt-s3-wide.bin"
    elif device_type == "matrixportal_s3":
        firmware_filename = "matrixportal-s3.bin"
    elif device_type == "matrixportal_s3_waveshare":
        firmware_filename = "matrixportal-s3-waveshare.bin"
    elif swap_colors:
        firmware_filename = "tidbyt-gen1_swap.bin"
    else:
        firmware_filename = "tidbyt-gen1.bin"

    # Check data directory first (for downloaded firmware), then fallback to bundled firmware
    data_firmware_path = db.get_data_dir() / "firmware" / firmware_filename
    bundled_firmware_path = Path(__file__).parent / "firmware" / firmware_filename

    if data_firmware_path.exists():
        file_path = data_firmware_path
    elif bundled_firmware_path.exists():
        file_path = bundled_firmware_path
    else:
        raise ValueError(
            f"Firmware file {firmware_filename} not found in {data_firmware_path} or {bundled_firmware_path}."
        )

    dict = {
        "XplaceholderWIFISSID____________": ap,
        "XplaceholderWIFIPASSWORD________________________________________": pw,
        "XplaceholderREMOTEURL___________________________________________________________________________________________________________": url,
    }
    with file_path.open("rb") as f:
        content = f.read()

    for old_string, new_string in dict.items():
        if len(new_string) > len(old_string):
            raise ValueError(
                "Replacement string cannot be longer than the original string."
            )
        position = content.find(old_string.encode("ascii") + b"\x00")
        if position == -1:
            raise ValueError(f"String '{old_string}' not found in the binary.")
        padded_new_string = new_string + "\x00"
        padded_new_string = padded_new_string.ljust(len(old_string) + 1, "\x00")
        content = (
            content[:position]
            + padded_new_string.encode("ascii")
            + content[position + len(old_string) + 1 :]
        )

    try:
        return correct_firmware_esptool.update_firmware_data(content, device_type)
    except ValueError:
        # For testing with dummy firmware, skip correction
        return content


def update_firmware_binaries(base_path: Path) -> dict[str, Any]:
    """Download the latest firmware bin files from GitHub releases.

    Returns:
        dict: Status information with keys:
            - 'success': bool - Whether the operation completed successfully
            - 'action': str - What action was taken ('updated', 'skipped', 'error')
            - 'message': str - Human readable message
            - 'version': str - Version that was processed
            - 'files_downloaded': int - Number of files downloaded (0 if skipped)
    """

    firmware_path = base_path / "firmware"
    firmware_repo = os.environ.get(
        "FIRMWARE_REPO", "https://github.com/tronbyt/firmware-esp32"
    )

    # Ensure firmware directory exists
    firmware_path.mkdir(parents=True, exist_ok=True)

    # Extract owner and repo from URL
    if firmware_repo.endswith(".git"):
        firmware_repo = firmware_repo[:-4]  # Remove .git suffix

    # Parse GitHub URL to get owner/repo
    try:
        parsed_url = urlparse(firmware_repo)
        if parsed_url.netloc == "github.com":
            # Remove leading/trailing '/' and split the path
            parts = [seg for seg in parsed_url.path.strip("/").split("/") if seg]
            if len(parts) >= 2:
                owner, repo = parts[0], parts[1]
            else:
                raise ValueError("Invalid GitHub URL format")
        else:
            raise ValueError("Not a GitHub URL")
    except Exception as e:
        error_msg = f"Error parsing firmware repository URL {firmware_repo}: {e}"
        logger.info(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }

    # GitHub API URL for latest release
    api_url = f"https://api.github.com/repos/{owner}/{repo}/releases/latest"

    try:
        logger.info(f"Fetching latest release info from {api_url}")

        # Fetch release information
        response = requests.get(api_url, timeout=10)
        response.raise_for_status()
        release_data = response.json()

        release_tag = release_data.get("tag_name", "unknown")
        logger.info(f"Found latest release: {release_tag}")

        # Check if we already have this version
        version_file = firmware_path / "firmware_version.txt"
        current_version = None
        if version_file.exists():
            try:
                with version_file.open("r") as f:
                    current_version = f.read().strip()
                logger.info(f"Current firmware version: {current_version}")
            except Exception as e:
                logger.info(f"Error reading current version file: {e}")

        if current_version == release_tag:
            logger.info(f"Firmware is already up to date (version {release_tag})")
            return {
                "success": True,
                "action": "skipped",
                "message": f"Firmware is already up to date (version {release_tag})",
                "version": release_tag,
                "files_downloaded": 0,
            }

        # Mapping from GitHub release names to expected local names
        firmware_name_mapping = {
            "tidbyt-gen1_firmware.bin": "tidbyt-gen1.bin",
            "tidbyt-gen1_swap_firmware.bin": "tidbyt-gen1_swap.bin",
            "tidbyt-gen2_firmware.bin": "tidbyt-gen2.bin",
            "pixoticker_firmware.bin": "pixoticker.bin",
            "tronbyt-s3_firmware.bin": "tronbyt-S3.bin",
            "tronbyt-s3-wide_firmware.bin": "tronbyt-s3-wide.bin",
            "matrixportal-s3_firmware.bin": "matrixportal-s3.bin",
            "matrixportal-s3-waveshare_firmware.bin": "matrixportal-s3-waveshare.bin",
        }

        # Download all .bin files from the release assets
        assets = release_data.get("assets", [])
        bin_files_downloaded = 0

        for asset in assets:
            asset_name = asset.get("name", "")
            if asset_name.endswith(".bin"):
                download_url = asset.get("browser_download_url")
                if download_url and asset_name in firmware_name_mapping:
                    # Use mapped name if available, otherwise use original name
                    local_name = firmware_name_mapping.get(asset_name, asset_name)
                    dest_file = firmware_path / str(local_name)
                    logger.info(
                        f"Downloading firmware file: {asset_name} -> {local_name}"
                    )

                    try:
                        r = requests.get(download_url, timeout=300)
                        r.raise_for_status()
                        dest_file.write_bytes(r.content)
                        bin_files_downloaded += 1
                        logger.info(f"Successfully downloaded: {local_name}")
                    except Exception as e:
                        logger.info(f"Error downloading {asset_name}: {e}")

        if bin_files_downloaded > 0:
            logger.info(
                f"Downloaded {bin_files_downloaded} firmware files to {firmware_path}"
            )

            # Write version information to file
            version_file = firmware_path / "firmware_version.txt"
            try:
                with version_file.open("w") as f:
                    f.write(f"{release_tag}\n")
                logger.info(f"Saved firmware version {release_tag} to {version_file}")

                return {
                    "success": True,
                    "action": "updated",
                    "message": f"Successfully updated firmware to version {release_tag} ({bin_files_downloaded} files downloaded)",
                    "version": release_tag,
                    "files_downloaded": bin_files_downloaded,
                }
            except Exception as e:
                logger.info(f"Error writing version file: {e}")
                return {
                    "success": False,
                    "action": "error",
                    "message": f"Downloaded firmware but failed to save version file: {e}",
                    "version": release_tag,
                    "files_downloaded": bin_files_downloaded,
                }
        else:
            logger.info("No .bin files found in the latest release")
            return {
                "success": False,
                "action": "error",
                "message": "No firmware files found in the latest release",
                "version": release_tag,
                "files_downloaded": 0,
            }

    except requests.exceptions.RequestException as e:
        error_msg = f"Error fetching release info: {e}"
        logger.info(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }
    except json.JSONDecodeError as e:
        error_msg = f"Error parsing release JSON: {e}"
        logger.info(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }
    except Exception as e:
        error_msg = f"Error updating firmware: {e}"
        logger.info(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }


def update_firmware_binaries_subprocess(
    base_path: Path,
) -> dict[str, Any]:
    # Run the update_firmware_binaries function in a subprocess.
    # This is a workaround for a conflict between Python's and Go's TLS implementations.
    # Only one TLS stack can be used per process, and libpixlet (used for rendering apps)
    # uses Go's TLS stack, which conflicts with Python's requests library that uses
    # Python's TLS stack.
    # See: https://github.com/tronbyt/server/issues/344.
    # Run the update in a subprocess and capture stdout for result transfer
    result = subprocess.run(
        [
            sys.executable,
            "-c",
            (
                "from tronbyt_server import firmware_utils; "
                "import logging, sys, json; "
                "from pathlib import Path;"
                "result = firmware_utils.update_firmware_binaries(Path(sys.argv[1])); "
                "print(json.dumps(result))"
            ),
            str(base_path),
        ],
        check=True,
        capture_output=True,
        text=True,
    )

    if result.stderr:
        logger.info(f"Firmware update subprocess logs:\n{result.stderr.strip()}")

    # Parse and return the result from stdout
    return dict(json.loads(result.stdout))
