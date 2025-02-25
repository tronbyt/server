# clone system repo and generate the apps.json list. will pullapp descrption pulled from the yaml if available
import json
import os
import shutil
import subprocess

import yaml

system_apps_path = "system-apps"
system_apps_repo = (
    os.environ.get("SYSTEM_APPS_REPO") or "https://github.com/Tronbyt/apps.git"
)
# check for existence of apps_path dir
if os.path.exists(system_apps_path):
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
for root, dirs, files in os.walk(system_apps_path):
    for file in files:
        if file.endswith(".star"):
            apps.append(os.path.join(root, file))
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
        app_basename = os.path.basename(app).replace(".star", "")
        app_dict["name"] = app_basename
        app_dict["path"] = app
        # "{}/apps/{}/{}.star".format(system_apps_path, app.replace('_',''), app)
        app_path = app

        # skip any files that include secret.star module and
        with open(app_path, "r") as f:
            app_str = f.read()
            if "secret.star" in app_str:
                # print("skipping {} (uses secret.star)".format(app))
                skip_count += 1
                continue

        app_base_path = ("/").join(app_path.split("/")[0:-1])
        yaml_path = "{}/manifest.yaml".format(app_base_path)
        static_images_path = "tronbyt_server/static/images"

        # check for existence of yaml_path
        if os.path.exists(yaml_path):
            with open(yaml_path, "r") as f:
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
            image_path = os.path.join(app_base_path, image_name)
            static_image_path = os.path.join(
                static_images_path, f"{app_basename}{os.path.splitext(image_name)[1]}"
            )

            # less than a meg only
            if (
                os.path.exists(image_path)
                and os.path.getsize(image_path) < 1 * 1024 * 1024
            ):
                # print(f"copying {image_path}")
                if not os.path.exists(static_image_path):
                    print(f"copying preview to static dir {static_image_path}")
                    new_previews += 1
                    shutil.copy(image_path, static_image_path)

            # set the preview for the app to the static preview location
            if os.path.exists(static_image_path):
                num_previews += 1
                app_dict["preview"] = os.path.basename(static_image_path)
                break

        count += 1
        apps_array.append(app_dict)
    except Exception as e:
        print("skipped " + app + " due to error: " + repr(e))
# print(apps_array)

# writeout apps_array as a json file
print(f"got {count} useable apps")
print(f"skipped {skip_count} secrets.star using apps")
print(f"copied {new_previews} new previews into static")
print(f"total previews found {num_previews}")
with open("system-apps.json", "w") as f:
    json.dump(apps_array, f)
