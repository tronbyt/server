# clone system repo and generate the apps.json list. will pullapp descrption pulled from the yaml if available
import json,os,sys,subprocess,shutil

system_apps_path = "system-apps"
system_apps_repo = os.environ.get('SYSTEM_APPS_REPO') or "https://github.com/tavdog/tronbyt-apps.git"
# check for existence of apps_path dir
if not os.path.exists(system_apps_path):
    print("{} not found, cloning {}".format(system_apps_path,system_apps_repo))

    result = subprocess.run(
                        [
                            "git",
                            "clone",
                            system_apps_repo,
                            system_apps_path,
                            "--depth",
                            "1"
                        ]
                    )
    if result.returncode != 0:
        print("Error Cloning Repo")
    else:
        print("Repo Cloned")

# run a command to generate a txt file withh all the .star file in the apps_path directory
command = [ "find", system_apps_path, "-name", "*.star" ]
output = subprocess.check_output(command, text=True)
print("got find output of {}".format(output))

# pull in the apps.txt list
apps_array = []
apps = output.split('\n')
apps.sort()
count = 0
for app in apps:
    print(app)
    try:
        # read in the file from system_apps_path/apps/
        app_dict = dict()
        app_dict['name'] = os.path.basename(app).replace('.star','')
        app_dict['path'] = app
        app_path = app #"{}/apps/{}/{}.star".format(system_apps_path, app.replace('_',''), app)

        # skip any files that include secret.star module and
        with open(app_path,'r') as f:
            app_str = f.read()
            if "secret.star" in app_str:
                print("skipping {} (uses secret.star)".format(app))
                continue
            if "summary:" in app_str:
                # loop though lines and pick out the summary line
                for line in app_str.split('\n'):
                    if "summary:" in line:
                        app_dict['summary'] = line.split(': ')[1]

        app_base_path = ("/").join(app_path.split('/')[0:-1])
        yaml_path = "{}/manifest.yaml".format(app_base_path)
        static_images_path = "tidbyt_manager/static/images"

        # check for existeanse of yaml_path
        if os.path.exists(yaml_path):
            with open(yaml_path,'r') as f:
                yaml_str = f.read()
                for line in yaml_str.split('\n'):
                    if "summary:" in line:
                        app_dict['summary'] = line.split(': ')[1]
        else:
            app_dict['summary'] = " -- "

        # Check for the existence of an image with the base name of the app name
        image_found = False
        for ext in ['gif','png','webp']:
            image_path = os.path.join(static_images_path, f"{app_dict['name']}.{ext}")
            # print(image_path)

            if os.path.exists(image_path) and os.path.getsize(image_path) < 1 * 1024 * 1024: # less than a meg only
                # Your code here
                app_dict["preview"] = os.path.basename(image_path)
                shutil.move(
                    image_path,
                    os.path.join(static_images_path, os.path.basename(image_path)),
                )
                image_found = True

                break

        # if not os.path.exists(os.path.join(static_images_path, os.path.basename(image_path))):
        #     # lets do a pixlet render and output to the images/static dir
        #     # command = [
        #     #     # "/pixlet/pixlet",
        #     #     "pixlet",
        #     #     "render",
        #     #     app_path,
        #     #     "-o",
        #     #     os.path.join(static_images_path, os.path.basename(image_path)),
        #     # ]
        #     # print(command)
        #     # result = subprocess.run(command)
        #     app_dict['preview'] = os.path.basename("not_found.jpg")  # Set a default image if not found
        # else:
        #     app_dict["preview"] = os.path.basename(image_path)
        count += 1
        apps_array.append(app_dict)
    except Exception as e:
        print("skipped " + app + " due to error: " + repr(e))
# print(apps_array)

# writeout apps_array as a json file
print(f"got {count} useable apps")
with open("system-apps.json",'w') as f:
    json.dump(apps_array,f)
