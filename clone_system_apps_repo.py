# clone system repo and generate the apps.json list. will pullapp descrption pulled from the yaml if available
import json
import os
import shutil
import subprocess
from pathlib import Path

import yaml

system_apps_path = Path("system-apps")
system_apps_repo = os.getenv("SYSTEM_APPS_REPO", "https://github.com/tronbyt/apps.git")

# check for existence of apps_path dir
if system_apps_path.exists():
    print("{} found, updating {}".format(system_apps_path, system_apps_repo))

    result = subprocess.run(["git", "pull", "--rebase=true"], cwd=system_apps_path)
    if result.returncode != 0:
        print("Error updating repo, whatevs")
    else:
        print("Repo updated")
else:
    print("{} not found, cloning {}".format(system_apps_path, system_apps_repo))

    result = subprocess.run(
        ["git", "clone", system_apps_repo, system_apps_path, "--depth", "1"]
    )
    if result.returncode != 0:
        print("Error Cloning Repo")
    else:
        print("Repo Cloned")

# find all the .star files in the apps_path directory
apps_array = []
apps = []
broken_apps = []

for file in system_apps_path.rglob("*.star"):
    apps.append(file)

broken_apps_path = system_apps_path / "broken_apps.txt"
if broken_apps_path.exists():
    print(f"processing broken_apps file {broken_apps_path}")
    try:
        with broken_apps_path.open("r") as f:
            broken_apps = f.read().splitlines()
            print(str(broken_apps))
    except Exception as e:
        print(f"problem reading broken_apps_txt {e}")

apps.sort()
count = 0
skip_count = 0
new_previews = 0
num_previews = 0
for app in apps:
    # print(app)
    try:
        # read in the file from system_apps_path/apps/
        app_dict = dict()
        app_basename = app.stem
        app_dict["name"] = app_basename
        app_dict["path"] = str(app)
        # "{}/apps/{}/{}.star".format(system_apps_path, app.replace('_',''), app)
        app_path = app

        # skip any broken apps
        if broken_apps and app.name in broken_apps:
            print(f"skipping broken app {app.name}")
            skip_count += 1
            continue

        # skip any files that include secret.star module and
        with app_path.open("r") as f:
            app_str = f.read()
            if "secret.star" in app_str:
                # print("skipping {} (uses secret.star)".format(app))
                skip_count += 1
                continue

        app_base_path = app_path.parent
        yaml_path = app_base_path / "manifest.yaml"
        static_images_path = Path("tronbyt_server") / "static" / "images"

        # check for existence of yaml_path
        if yaml_path.exists():
            with yaml_path.open("r") as f:
                yaml_dict = yaml.safe_load(f)
                app_dict.update(yaml_dict)
        else:
            app_dict["summary"] = "System App"

        # Check for a preview in the repo and copy it over to static previews directory
        for image_name in [
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
                # print(f"copying {image_path}")
                if not static_image_path.exists():
                    print(f"copying preview to static dir {static_image_path}")
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
        print("skipped " + str(app) + " due to error: " + repr(e))
# print(apps_array)

# writeout apps_array as a json file
print(f"got {count} useable apps")
print(f"skipped {skip_count} secrets.star using apps")
print(f"copied {new_previews} new previews into static")
print(f"total previews found {num_previews}")
with open("system-apps.json", "w") as f:
    json.dump(apps_array, f, indent=4)
