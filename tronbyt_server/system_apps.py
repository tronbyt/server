# clone system repo and generate the apps.json list. App description pulled from the YAML if available
import json
import os
import shutil
import subprocess
from pathlib import Path
from typing import Any, Optional

import yaml
from flask import current_app

from tronbyt_server.models.app import AppMetadata


def git_command(
    command: list[str], cwd: Optional[Path] = None, check: bool = False
) -> subprocess.CompletedProcess[bytes]:
    """Run a git command in the specified path."""
    env = os.environ.copy()
    env.setdefault("HOME", os.getcwd())
    return subprocess.run(command, cwd=cwd, env=env, check=check)


def log_message(message: str) -> None:
    """Log a message using Flask's logger if in app context, otherwise use print."""
    if current_app:
        current_app.logger.info(message)
    else:
        print(message)


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
    import urllib.request
    import json

    firmware_path = base_path / "firmware"
    firmware_repo = os.getenv(
        "FIRMWARE_REPO", "https://github.com/tronbyt/firmware-esp32"
    )

    # Ensure firmware directory exists
    firmware_path.mkdir(parents=True, exist_ok=True)

    # Extract owner and repo from URL
    if firmware_repo.endswith(".git"):
        firmware_repo = firmware_repo[:-4]  # Remove .git suffix

    # Parse GitHub URL to get owner/repo
    try:
        if "github.com/" in firmware_repo:
            parts = firmware_repo.split("github.com/")[1].split("/")
            if len(parts) >= 2:
                owner, repo = parts[0], parts[1]
            else:
                raise ValueError("Invalid GitHub URL format")
        else:
            raise ValueError("Not a GitHub URL")
    except Exception as e:
        error_msg = f"Error parsing firmware repository URL {firmware_repo}: {e}"
        log_message(error_msg)
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
        log_message(f"Fetching latest release info from {api_url}")

        # Fetch release information
        with urllib.request.urlopen(api_url) as response:
            release_data = json.loads(response.read().decode())

        release_tag = release_data.get("tag_name", "unknown")
        log_message(f"Found latest release: {release_tag}")

        # Check if we already have this version
        version_file = firmware_path / "firmware_version.txt"
        current_version = None
        if version_file.exists():
            try:
                with version_file.open("r") as f:
                    current_version = f.read().strip()
                log_message(f"Current firmware version: {current_version}")
            except Exception as e:
                log_message(f"Error reading current version file: {e}")

        if current_version == release_tag:
            log_message(f"Firmware is already up to date (version {release_tag})")
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
                    dest_file = firmware_path / local_name
                    log_message(
                        f"Downloading firmware file: {asset_name} -> {local_name}"
                    )

                    try:
                        urllib.request.urlretrieve(download_url, dest_file)
                        bin_files_downloaded += 1
                        log_message(f"Successfully downloaded: {local_name}")
                    except Exception as e:
                        log_message(f"Error downloading {asset_name}: {e}")

        if bin_files_downloaded > 0:
            log_message(
                f"Downloaded {bin_files_downloaded} firmware files to {firmware_path}"
            )

            # Write version information to file
            version_file = firmware_path / "firmware_version.txt"
            try:
                with version_file.open("w") as f:
                    f.write(f"{release_tag}\n")
                log_message(f"Saved firmware version {release_tag} to {version_file}")

                return {
                    "success": True,
                    "action": "updated",
                    "message": f"Successfully updated firmware to version {release_tag} ({bin_files_downloaded} files downloaded)",
                    "version": release_tag,
                    "files_downloaded": bin_files_downloaded,
                }
            except Exception as e:
                log_message(f"Error writing version file: {e}")
                return {
                    "success": False,
                    "action": "error",
                    "message": f"Downloaded firmware but failed to save version file: {e}",
                    "version": release_tag,
                    "files_downloaded": bin_files_downloaded,
                }
        else:
            log_message("No .bin files found in the latest release")
            return {
                "success": False,
                "action": "error",
                "message": "No firmware files found in the latest release",
                "version": release_tag,
                "files_downloaded": 0,
            }

    except urllib.error.HTTPError as e:
        error_msg = f"HTTP error fetching release info: {e.code} {e.reason}"
        log_message(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }
    except urllib.error.URLError as e:
        error_msg = f"URL error fetching release info: {e.reason}"
        log_message(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }
    except json.JSONDecodeError as e:
        error_msg = f"Error parsing release JSON: {e}"
        log_message(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }
    except Exception as e:
        error_msg = f"Error updating firmware: {e}"
        log_message(error_msg)
        return {
            "success": False,
            "action": "error",
            "message": error_msg,
            "version": "unknown",
            "files_downloaded": 0,
        }


def update_system_repo(base_path: Path) -> None:
    system_apps_path = base_path / "system-apps"
    system_apps_url = os.getenv(
        "SYSTEM_APPS_REPO", "https://github.com/tronbyt/apps.git"
    )

    # Check if the URL contains a branch specification
    if "@" in system_apps_url and ".git@" in system_apps_url:
        system_apps_repo, branch_name = system_apps_url.rsplit("@", 1)
    else:
        system_apps_repo = system_apps_url
        branch_name = None

    # check for existence of .git directory
    git_dir = system_apps_path / ".git"
    log_message(f"Checking for git directory at: {git_dir}")

    # If running as root, add the system-apps directory to the safe.directory list
    if os.geteuid() == 0:  # Check if the script is running as root
        git_command(
            [
                "git",
                "config",
                "--global",
                "--add",
                "safe.directory",
                str(system_apps_path),
            ],
            check=True,
        )

    if git_dir.is_dir():  # Check if it's actually a directory
        log_message(f"{system_apps_path} git repo found, updating {system_apps_repo}")

        result = git_command(["git", "pull", "--rebase=true"], cwd=system_apps_path)
        if result.returncode != 0:
            log_message(f"Error updating repository. Return code: {result.returncode}")
        else:
            log_message("Repo updated")
    else:
        log_message(
            f"Git repo not found in {system_apps_path}, cloning {system_apps_repo}"
        )

        if branch_name:
            # Use specific branch clone command
            result = git_command(
                [
                    "git",
                    "clone",
                    "--branch",
                    branch_name,
                    "--single-branch",
                    "--depth",
                    "1",
                    system_apps_repo,
                    str(system_apps_path),
                ]
            )
        else:
            # Use default clone command
            result = git_command(
                [
                    "git",
                    "clone",
                    system_apps_repo,
                    str(system_apps_path),
                    "--depth",
                    "1",
                ]
            )
        if result.returncode != 0:
            log_message("Error Cloning Repo")
        else:
            log_message("Repo Cloned")

    # find all the .star files in the apps_path directory
    apps_array = []
    apps = list(system_apps_path.rglob("*.star"))
    broken_apps = []

    broken_apps_path = system_apps_path / "broken_apps.txt"
    if broken_apps_path.exists():
        log_message(f"processing broken_apps file {broken_apps_path}")
        try:
            broken_apps = broken_apps_path.read_text().splitlines()
            log_message(str(broken_apps))
        except Exception as e:
            log_message(f"problem reading broken_apps_txt {e}")

    apps.sort()
    count = 0
    skip_count = 0
    new_previews = 0
    num_previews = 0
    static_images_path = base_path / "apps"
    os.makedirs(static_images_path, exist_ok=True)
    for app in apps:
        try:
            app_basename = app.stem
            app_dict = AppMetadata(
                name=app_basename,
                path=str(app),
            )

            # skip any broken apps
            if broken_apps and app.name in broken_apps:
                log_message(f"skipping broken app {app.name}")
                skip_count += 1
                continue

            # skip any files that include secret.star module
            app_str = app.read_text()
            if "secret.star" in app_str:
                skip_count += 1
                continue

            app_base_path = app.parent
            yaml_path = app_base_path / "manifest.yaml"

            # check for existence of yaml_path
            if yaml_path.exists():
                with yaml_path.open("r") as f:
                    yaml_dict = yaml.safe_load(f)
                    app_dict.update(yaml_dict)
            else:
                app_dict["summary"] = "System App"
            package_name = app_dict.get("packageName", app_base_path.name)

            # Check for a preview in the repo and copy it over to static previews directory
            for image_name in [
                f"{package_name}.webp",
                f"{package_name}.gif",
                f"{package_name}.png",
                f"{app_basename}.webp",
                f"{app_basename}.gif",
                f"{app_basename}.png",
                "screenshot.webp",
                "screenshot.gif",
                "screenshot.png",
            ]:
                image_path = app_base_path / image_name
                static_image_path = (
                    static_images_path / f"{app_basename}{image_path.suffix}"
                )

                # less than a meg only
                if image_path.exists() and image_path.stat().st_size < 1 * 1024 * 1024:
                    if not static_image_path.exists():
                        log_message(
                            f"copying preview to static dir {static_image_path}"
                        )
                        new_previews += 1
                        shutil.copy(image_path, static_image_path)

                # set the preview for the app to the static preview location
                if static_image_path.exists():
                    num_previews += 1
                    app_dict["preview"] = static_image_path.name
                    break
            count += 1
            apps_array.append(app_dict)
        except Exception as e:
            log_message(f"skipped {app} due to error: {repr(e)}")

    # writeout apps_array as a json file
    log_message(f"got {count} useable apps")
    log_message(f"skipped {skip_count} secrets.star using apps")
    log_message(f"copied {new_previews} new previews into static")
    log_message(f"total previews found {num_previews}")
    with (base_path / "system-apps.json").open("w") as f:
        json.dump(apps_array, f, indent=4)


if __name__ == "__main__":
    update_system_repo(Path(os.getcwd()))
