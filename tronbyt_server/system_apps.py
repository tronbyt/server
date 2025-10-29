"""Utilities for managing the system apps repository."""

# clone system repo and generate the apps.json list. App description pulled from the YAML if available
import json
import logging
import os
import shutil
import subprocess
import yaml
from datetime import datetime
from pathlib import Path
from typing import Optional

from tronbyt_server.config import get_settings
from tronbyt_server.models import AppMetadata


def git_command(
    command: list[str],
    cwd: Optional[Path] = None,
    check: bool = False,
    capture_output: bool = False,
) -> subprocess.CompletedProcess[bytes]:
    """Run a git command in the specified path."""
    env = os.environ.copy()
    env.setdefault("HOME", os.getcwd())
    return subprocess.run(
        command, cwd=cwd, env=env, check=check, capture_output=capture_output
    )


def get_system_repo_info(base_path: Path) -> dict[str, Optional[str]]:
    """Get information about the current system repo commit.

    Returns:
        dict with keys: 'commit_hash', 'commit_url', 'repo_url', 'branch'
    """
    system_apps_path = base_path / "system-apps"
    system_apps_url = get_settings().SYSTEM_APPS_REPO

    # Parse the repo URL and branch
    if "@" in system_apps_url and ".git@" in system_apps_url:
        system_apps_repo, branch_name = system_apps_url.rsplit("@", 1)
    else:
        system_apps_repo = system_apps_url
        branch_name = "main"

    # Remove .git suffix for web URL
    repo_web_url = system_apps_repo.replace(".git", "")

    info = {
        "commit_hash": None,
        "commit_url": None,
        "repo_url": repo_web_url,
        "branch": branch_name,
    }

    # Get the current commit hash
    git_dir = system_apps_path / ".git"
    if git_dir.is_dir():
        try:
            result = git_command(
                ["git", "rev-parse", "HEAD"], cwd=system_apps_path, capture_output=True
            )
            if result.returncode == 0:
                commit_hash = result.stdout.decode().strip()
                info["commit_hash"] = commit_hash[:7]  # Short hash
                info["commit_url"] = f"{repo_web_url}/tree/{commit_hash}"
        except Exception:
            pass

    return info


def generate_apps_json(base_path: Path, logger: logging.Logger) -> None:
    """Generate the system-apps.json file from the system-apps directory.

    This function only processes the apps and generates the JSON file.
    It does NOT do a git pull - use update_system_repo() for that.
    """
    system_apps_path = base_path / "system-apps"

    # find all the .star files in the apps_path directory
    apps_array = []
    apps = list(system_apps_path.rglob("*.star"))
    broken_apps = []

    broken_apps_path = system_apps_path / "broken_apps.txt"
    if broken_apps_path.exists():
        logger.info(f"processing broken_apps file {broken_apps_path}")
        try:
            broken_apps = broken_apps_path.read_text().splitlines()
            logger.info(str(broken_apps))
        except Exception as e:
            logger.info(f"problem reading broken_apps_txt {e}")

    apps.sort()
    count = 0
    skip_count = 0
    new_previews = 0
    num_previews = 0
    static_images_path = base_path / "apps"
    os.makedirs(static_images_path, exist_ok=True)

    # Get all git commit dates in a single efficient command
    git_dates = {}
    try:
        # Use git log with --name-only and --diff-filter to get last modification date for each file
        # This processes all files in one command which is much faster
        result = subprocess.run(
            [
                "git",
                "log",
                "--name-only",
                "--pretty=format:%ci",
                "--diff-filter=ACMR",
                "--",
                "*.star",
            ],
            cwd=system_apps_path,
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode == 0 and result.stdout:
            lines = result.stdout.strip().split("\n")
            current_date = None
            for line in lines:
                line = line.strip()
                if not line:
                    continue
                # Check if this is a date line (contains timezone offset pattern)
                if len(line) > 20 and line[0:4].isdigit() and line[4] == "-":
                    try:
                        # Parse git date format: "2024-03-15 14:30:45 -0700"
                        git_date_str = line.split()[0:2]
                        current_date = datetime.strptime(
                            " ".join(git_date_str), "%Y-%m-%d %H:%M:%S"
                        )
                    except (ValueError, IndexError):
                        pass
                elif line.endswith(".star") and current_date:
                    # This is a filename - get just the basename
                    filename = Path(line).name
                    # Only store the first (most recent) date we see for each file
                    if filename not in git_dates:
                        git_dates[filename] = current_date
        logger.info(f"Retrieved git commit dates for {len(git_dates)} apps")
    except (subprocess.TimeoutExpired, subprocess.SubprocessError) as e:
        logger.warning(
            f"Failed to get git dates in bulk, will use file modification times: {e}"
        )

    for app in apps:
        try:
            app_basename = app.stem
            # Use git date if available, otherwise fall back to file modification time
            if app.name in git_dates:
                mod_time = git_dates[app.name]
            else:
                mod_time = datetime.fromtimestamp(app.stat().st_mtime)

            app_dict = AppMetadata(
                name=app_basename,
                fileName=app.name,  # Store the actual filename with .star extension
                path=str(app),
                date=mod_time.strftime("%Y-%m-%d %H:%M"),
            )

            # Check if app is broken
            is_broken = broken_apps and app.name in broken_apps
            if is_broken:
                logger.info(f"marking broken app {app.name}")
                app_dict.broken = True
                app_dict.brokenReason = "Marked Broken"
                skip_count += 1

            # Check if app uses secret.star module
            app_str = app.read_text()
            if "secret.star" in app_str:
                logger.info(f"marking app {app.name} as broken (uses secret.star)")
                app_dict.broken = True
                app_dict.brokenReason = "Requires Secrets"
                skip_count += 1

            app_base_path = app.parent
            yaml_path = app_base_path / "manifest.yaml"

            # check for existence of yaml_path
            if yaml_path.exists():
                with yaml_path.open("r") as f:
                    yaml_dict = yaml.safe_load(f)
                    # Merge YAML dict into Pydantic model
                    app_dict = app_dict.model_copy(
                        update={
                            k: v for k, v in yaml_dict.items() if hasattr(app_dict, k)
                        }
                    )
            else:
                app_dict.summary = "System App"

            package_name = app_dict.packageName or app_base_path.name

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
                        logger.info(
                            f"copying preview to static dir {static_image_path}"
                        )
                        new_previews += 1
                        shutil.copy(image_path, static_image_path)

                # set the preview for the app to the static preview location
                if static_image_path.exists():
                    num_previews += 1
                    app_dict.preview = static_image_path.name
                    break
            count += 1
            apps_array.append(app_dict)
        except Exception as e:
            logger.info(f"skipped {app} due to error: {repr(e)}")

    # writeout apps_array as a json file
    logger.info(f"got {count} useable apps")
    logger.info(f"skipped {skip_count} secrets.star using apps")
    logger.info(f"copied {new_previews} new previews into static")
    logger.info(f"total previews found {num_previews}")
    with (base_path / "system-apps.json").open("w") as f:
        json.dump([a.model_dump() for a in apps_array], f, indent=4)


def update_system_repo(base_path: Path, logger: logging.Logger) -> None:
    """Update the system apps repository and regenerate the apps.json file.

    This function:
    1. Does a git pull (or clone if needed)
    2. Calls generate_apps_json() to process the apps
    """
    system_apps_path = base_path / "system-apps"
    system_apps_url = get_settings().SYSTEM_APPS_REPO

    # Check if the URL contains a branch specification
    if "@" in system_apps_url and ".git@" in system_apps_url:
        system_apps_repo, branch_name = system_apps_url.rsplit("@", 1)
    else:
        system_apps_repo = system_apps_url
        branch_name = None

    # check for existence of .git directory
    git_dir = system_apps_path / ".git"
    logger.info(f"Checking for git directory at: {git_dir}")

    # If running as root, add the system-apps directory to the safe.directory list
    if os.geteuid() == 0:  # Check if the script is running as root
        # Check if the directory is already in the safe.directory list
        result = git_command(
            ["git", "config", "--global", "--get-all", "safe.directory"],
            capture_output=True,
        )
        safe_dirs = result.stdout.decode().strip().splitlines()
        if str(system_apps_path) not in safe_dirs:
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
        logger.info(f"{system_apps_path} git repo found, updating {system_apps_repo}")

        # Check if there are local changes to broken_apps.txt
        status_result = git_command(
            ["git", "status", "--porcelain", "broken_apps.txt"],
            cwd=system_apps_path,
            capture_output=True,
        )
        has_local_changes = (
            status_result.returncode == 0 and status_result.stdout.strip()
        )

        if has_local_changes:
            logger.info(
                "Local changes detected in broken_apps.txt, stashing before pull"
            )
            # Stash only broken_apps.txt
            stash_result = git_command(
                [
                    "git",
                    "stash",
                    "push",
                    "-m",
                    "Auto-stash broken_apps.txt",
                    "broken_apps.txt",
                ],
                cwd=system_apps_path,
            )
            if stash_result.returncode != 0:
                logger.warning("Failed to stash broken_apps.txt changes")

        result = git_command(["git", "pull", "--rebase=true"], cwd=system_apps_path)
        if result.returncode != 0:
            logger.info(f"Error updating repository. Return code: {result.returncode}")
        else:
            logger.info("Repo updated")

        # Re-apply stashed changes if we stashed them
        if has_local_changes and stash_result.returncode == 0:
            logger.info("Re-applying stashed broken_apps.txt changes")
            pop_result = git_command(["git", "stash", "pop"], cwd=system_apps_path)
            if pop_result.returncode != 0:
                logger.warning(
                    "Failed to re-apply stashed changes, they remain in stash"
                )
            else:
                logger.info("Successfully re-applied local broken_apps.txt changes")
    else:
        logger.info(
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
            logger.info("Error Cloning Repo")
        else:
            logger.info("Repo Cloned")

    # Now generate the apps.json file
    generate_apps_json(base_path, logger)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    logger = logging.getLogger("system_apps")
    update_system_repo(Path(os.getcwd()), logger)
