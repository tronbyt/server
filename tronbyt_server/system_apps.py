"""Utilities for managing the system apps repository."""

# clone system repo and generate the apps.json list. App description pulled from the YAML if available
import json
import logging
import os
import shutil
import yaml
from datetime import datetime
from pathlib import Path

from git import Git, GitCommandError, Repo

from tronbyt_server.config import get_settings
from tronbyt_server.models import AppMetadata
from tronbyt_server.git_utils import get_primary_remote, get_repo

logger = logging.getLogger(__name__)


def get_system_repo_info(base_path: Path) -> dict[str, str | None]:
    """Get information about the current system repo commit.

    Returns:
        dict with keys: 'commit_hash', 'commit_url', 'repo_url', 'branch'
    """
    system_apps_path = base_path / "system-apps"
    repo = get_repo(system_apps_path)
    if not repo:
        return {
            "commit_hash": None,
            "commit_url": None,
            "repo_url": None,
            "branch": None,
        }

    repo_web_url = None
    branch_name = None
    commit_hash = None
    commit_url = None

    try:
        remote = get_primary_remote(repo)
        if remote:
            repo_url = remote.url
            repo_web_url = repo_url.replace(".git", "")
    except GitCommandError as e:
        logger.warning(f"Could not get remote URL from {system_apps_path}: {e}")

    try:
        branch_name = repo.active_branch.name
    except TypeError:
        # Detached HEAD
        branch_name = "DETACHED"
    except GitCommandError as e:
        logger.warning(f"Could not get branch name from {system_apps_path}: {e}")

    try:
        commit_hash = repo.head.commit.hexsha
        if repo_web_url:
            commit_url = f"{repo_web_url}/tree/{commit_hash}"
    except ValueError:  # Handles empty repo
        logger.warning(
            f"Could not get commit hash from {system_apps_path}, repo may be empty."
        )
    except GitCommandError as e:
        logger.warning(f"Could not get commit hash from {system_apps_path}: {e}")

    return {
        "commit_hash": commit_hash[:7] if commit_hash else None,
        "commit_url": commit_url,
        "repo_url": repo_web_url,
        "branch": branch_name,
    }


def generate_apps_json(base_path: Path) -> None:
    """Generate the system-apps.json file from the system-apps directory.

    This function only processes the apps and generates the JSON file.
    It does NOT do a git pull - use update_system_repo() for that.
    """
    system_apps_path = base_path / "system-apps"
    repo = get_repo(system_apps_path)

    # find all the .star files in the apps_path directory
    apps_array: list[AppMetadata] = []
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
    new_previews_2x = 0
    num_previews_2x = 0
    static_images_path = base_path / "apps"
    os.makedirs(static_images_path, exist_ok=True)
    MAX_PREVIEW_SIZE_BYTES = 1 * 1024 * 1024  # 1MB

    git_dates: dict[str, datetime] = {}
    if repo:
        try:
            # Use git log with --name-only and --diff-filter to get last modification date for each file
            # This processes all files in one command which is much faster
            log_output = repo.git.log(
                "--name-only",
                "--pretty=format:%ci",
                "--diff-filter=ACMR",
                "--",
                "*.star",
                kill_after_timeout=30,
            )
            if log_output:
                lines = log_output.strip().split("\n")
                current_date = None
                for line in lines:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        # Attempt to parse the line as a date. Format is "YYYY-MM-DD HH:MM:SS +/-ZZZZ"
                        git_date_str = line.split()[0:2]
                        current_date = datetime.strptime(
                            " ".join(git_date_str), "%Y-%m-%d %H:%M:%S"
                        )
                    except (ValueError, IndexError):
                        # If parsing fails, it's a file path.
                        if line.endswith(".star") and current_date:
                            filename = Path(line).name
                            # Only store the first (most recent) date we see for each file
                            if filename not in git_dates:
                                git_dates[filename] = current_date
            logger.info(f"Retrieved git commit dates for {len(git_dates)} apps")
        except GitCommandError as e:
            logger.warning(f"Failed to get git dates in bulk: {e}")

    for app in apps:
        try:
            app_basename = app.stem
            # Use git date if available, otherwise fall back to file modification time
            if app.name in git_dates:
                mod_time: datetime = git_dates[app.name]
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
            for image_name_base in [package_name, app_basename, "screenshot"]:
                if app_dict.preview:
                    break
                for ext in [".webp", ".gif", ".png"]:
                    image_name = f"{image_name_base}{ext}"
                    image_path = app_base_path / image_name
                    static_image_path = static_images_path / f"{app_basename}{ext}"

                    # less than a meg only
                    if (
                        image_path.exists()
                        and image_path.stat().st_size < MAX_PREVIEW_SIZE_BYTES
                    ):
                        if not static_image_path.exists():
                            logger.info(
                                f"copying preview to static dir {static_image_path}"
                            )
                            new_previews += 1
                            shutil.copy(image_path, static_image_path)

                        # set the preview for the app to the static preview location
                        if static_image_path.exists():
                            if not app_dict.preview:
                                num_previews += 1
                                app_dict.preview = static_image_path.name

                            # Now check for a @2x version of this found preview
                            image_name_2x = f"{image_name_base}@2x{ext}"
                            image_path_2x = app_base_path / image_name_2x
                            static_image_path_2x = (
                                static_images_path / f"{app_basename}@2x{ext}"
                            )

                            if (
                                image_path_2x.exists()
                                and image_path_2x.stat().st_size
                                < MAX_PREVIEW_SIZE_BYTES
                            ):
                                if not static_image_path_2x.exists():
                                    logger.info(
                                        "copying 2x preview to static dir"
                                        f" {static_image_path_2x}"
                                    )
                                    new_previews_2x += 1
                                    shutil.copy(image_path_2x, static_image_path_2x)

                            if static_image_path_2x.exists():
                                num_previews_2x += 1
                                app_dict.preview2x = static_image_path_2x.name

                            # Found preview and checked for 2x, break from ext loop
                            break
            count += 1
            apps_array.append(app_dict)
        except Exception as e:
            logger.info(f"skipped {app} due to error: {repr(e)}")

    # writeout apps_array as a json file
    logger.info(f"got {count} useable apps")
    logger.info(f"skipped {skip_count} secrets.star using apps")
    logger.info(f"copied {new_previews} new previews into static")
    logger.info(f"total previews found: {num_previews}")
    logger.info(f"copied {new_previews_2x} new 2x previews into static")
    logger.info(f"total 2x previews found: {num_previews_2x}")
    with (base_path / "system-apps.json").open("w") as f:
        json.dump([a.model_dump() for a in apps_array], f, indent=4)


def update_system_repo(base_path: Path) -> None:
    """Update the system apps repository and regenerate the apps.json file.

    This function:
    1. Does a git pull (or clone if needed)
    2. Calls generate_apps_json() to process the apps
    """
    system_apps_path = base_path / "system-apps"

    # If running as root, add the system-apps directory to the safe.directory list.
    # This must be done before any repo operations to prevent UnsafeRepositoryError.
    if os.geteuid() == 0:
        try:
            g = Git()
            # Check if the directory is already in the safe.directory list
            try:
                safe_dirs = g.config(
                    "--global", "--get-all", "safe.directory"
                ).splitlines()
            except GitCommandError:
                safe_dirs = []
            if str(system_apps_path) not in safe_dirs:
                logger.info(f"Adding {system_apps_path} to git safe.directory")
                g.config("--global", "--add", "safe.directory", str(system_apps_path))
        except GitCommandError as e:
            logger.warning(f"Could not configure safe.directory: {e}")

    repo = get_repo(system_apps_path)

    if repo:
        logger.info(f"{system_apps_path} git repo found, updating…")
        try:
            # Stash local changes to broken_apps.txt if any
            stashed = False
            if "broken_apps.txt" in repo.untracked_files or repo.is_dirty(
                path="broken_apps.txt"
            ):
                logger.info(
                    "Local changes detected in broken_apps.txt, stashing before pull"
                )
                repo.git.stash(
                    "push", "-m", "Auto-stash broken_apps.txt", "broken_apps.txt"
                )
                stashed = True

            remote = get_primary_remote(repo)
            if remote:
                remote.pull(rebase=True)
                logger.info("Repo updated")
            else:
                logger.warning(
                    f"No remote found to pull from for repo at {system_apps_path}"
                )

            if stashed:
                logger.info("Re-applying stashed broken_apps.txt changes")
                try:
                    repo.git.stash("pop")
                    logger.info("Successfully re-applied local broken_apps.txt changes")
                except GitCommandError:
                    logger.warning(
                        "Failed to re-apply stashed changes, they remain in stash"
                    )
        except (GitCommandError, AttributeError, IndexError) as e:
            logger.error(f"Error updating repository: {e}")
    else:
        system_apps_url = get_settings().SYSTEM_APPS_REPO
        if "@" in system_apps_url and ".git@" in system_apps_url:
            repo_url, branch = system_apps_url.rsplit("@", 1)
        else:
            repo_url = system_apps_url
            branch = None

        logger.info(f"Git repo not found in {system_apps_path}, cloning {repo_url}")
        try:
            if branch:
                Repo.clone_from(
                    repo_url,
                    system_apps_path,
                    branch=branch,
                    single_branch=True,
                    depth=1,
                )
            else:
                Repo.clone_from(repo_url, system_apps_path, depth=1)
            logger.info("Repo Cloned")
        except GitCommandError as e:
            logger.error(f"Error Cloning Repo: {e}")

    generate_apps_json(base_path)


if __name__ == "__main__":
    logging.basicConfig(level=logging.INFO)
    logger = logging.getLogger("system_apps")
    update_system_repo(Path(os.getcwd()))
