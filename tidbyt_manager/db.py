import os,json,subprocess,datetime
from werkzeug.security import check_password_hash, generate_password_hash
from werkzeug.utils import secure_filename
from flask import current_app
from datetime import datetime
import time
import sys
import shutil
import subprocess

def get_night_mode_is_active(device):
    current_hour = datetime.now().hour
    if device.get("night_start",-1) > -1:
        start_hour = device['night_start']
        end_hour = 6 # 6am
        if start_hour <= end_hour:  # Normal case (e.g., 9 to 17)
            if start_hour <= current_hour <= end_hour:
                print("nightmode active")
                return True
        else:  # Wrapped case (e.g., 22 to 6 - overnight)
            if current_hour >= start_hour or current_hour <= end_hour:
                print("nightmode active")
                return True
    return False

def get_device_brightness(device):
        if 'night_brightness' in device and get_night_mode_is_active(device):
            return int(device['night_brightness']*2)
        else:  # Wrapped case (e.g., 22 to 6 - overnight)
            return int(device.get("brightness",30)*2)

def brightness_int_from_string(brightness_string):
    brightness_mapping = { "dim": 10, "low": 20, "medium": 40, "high": 80 }
    brightness_value = brightness_mapping[brightness_string]  # Get the numerical value from the dictionary, default to 50 if not found
    return brightness_value

def get_users_dir():
    #print(f"users dir : {current_app.config['USERS_DIR']}")
    return current_app.config['USERS_DIR']

def get_user_config_path(user):
    return f"{get_users_dir()}/{user}/{user}.json"

def user_exists(username):
    try:
        with open(f"{get_users_dir()}/{username}/{username}.json") as file:
            # print("username: {} exists.".format(username))
            return True
    except:
        return False
    return False

def file_exists(file_path):
    if os.path.exists(file_path):
        return True
    else:
        return False

def get_user(username):
    try:
        with open(f"{get_users_dir()}/{username}/{username}.json") as file:
            user = json.load(file)
#            print("return user")
            return user
    except Exception as e:
        print("problem with get_user" + str(e))
        return False

def auth_user(username,password):
    try:
        with open(f"{get_users_dir()}/{username}/{username}.json") as file:
            user = json.load(file)
            print(user)
            if check_password_hash(user.get("password"), password):
                return user
            else:
                print("bad password")
                return False
    except:
        return False

def save_user(user):
     if "username" in user:
        username = user['username']
        try:
            with open(f"{get_users_dir()}/{username}/{username}.json","w") as file:
                json.dump(user,file)
            return True      
        except:
            print("couldn't save {}".format(user))
            return False
def create_user_dir(user):
    dir = sanitize(user)
    dir = secure_filename(dir)
    # test for directory named dir and if not exist creat it
    user_dir = f"{get_users_dir()}/{user}"
    if not os.path.exists(user_dir):
        os.makedirs(user_dir)
        os.makedirs(user_dir+"/configs")
        os.makedirs(user_dir+"/apps")

        return True
    else:
        return False

def get_apps_list(user):
    app_list = list()
    # test for directory named dir and if not exist creat it
    if user == "system" or user == "":
        list_file = "tidbyt-apps/apps.json"
 
        if not os.path.exists(list_file):
            print("Generating apps.json file...")
            subprocess.run(["python3", "gen_app_array.py"])
            print("apps.json file generated.")

        with open(list_file,'r') as f:
            return json.load(f)
    else:
        dir = "{}/{}/apps".format(get_users_dir(), user)
    if os.path.exists(dir):
        command = [ "find", dir, "-name", "*.star" ]
        output = subprocess.check_output(command, text=True)
        print("got find output of {}".format(output))
    
        apps_paths = output.split("\n")
        for app in apps_paths:
            if app == "":
                continue
            app_dict = dict()
            app_dict['path'] = app
            app = app.replace(dir+"/","")
            app = app.replace("\n","")
            app = app.replace('.star','')
            app_dict['name'] = app.split('/')[-1]

            # look for a yaml file
            app_base_path = ("/").join(app_dict['path'].split('/')[0:-1])
            yaml_path = "{}/manifest.yaml".format(app_base_path)
            print("checking for yaml in {}".format(yaml_path))
            # check for existeanse of yaml_path
            if os.path.exists(yaml_path):
                with open(yaml_path,'r') as f:
                    yaml_str = f.read()
                    for line in yaml_str.split('\n'):
                            if "summary:" in line:
                                app_dict['summary'] = line.split(': ')[1]
            else:
                app_dict['summary'] = "Custom App"
            app_list.append(app_dict)
        return app_list
    else:
        print("no apps list found for {}".format(user))
        return []

def get_app_details(user,name):
    # first look for the app name in the custom apps
    custom_apps = get_apps_list(user)
    print(user,name)
    for app in custom_apps:
        print(app)
        if app['name'] == name:
            # we found it
            return app
    # if we get here then the app is not in custom apps
    # so we need to look in the tidbyt-apps directory
    apps = get_apps_list("system")
    for app in apps:
        if app['name'] == name:
            return app
    return {}

def sanitize(str):
    str = str.replace(" ","_")
    str = str.replace("-","")
    str = str.replace(".","")
    str = str.replace("/","")
    str = str.replace("\\","")
    str = str.replace("%","")
    str = str.replace("'","")
    return str

from urllib.parse import quote, unquote

def sanitize_url(url):
    # Decode any percent-encoded characters
    url = unquote(url)
    # Replace spaces with underscores
    url = url.replace(" ", "_")
    # Remove unwanted characters
    for char in ["'", "\\"]:
        url = url.replace(char, "")
    # Encode back into a valid URL
    url = quote(url, safe="/:.?&=")  # Allow standard URL characters
    return url


# basically just call gen_apps_array.py script
def generate_apps_list():
    os.system("python3 gen_app_array.py") # safe
    print("generated apps list")

def allowed_file(filename):
    return '.' in filename and \
           filename.rsplit('.', 1)[1].lower() in ['star']

def save_user_app(file,path):
    filename = sanitize(file.filename)
    filename = secure_filename(filename)
    
    if file and allowed_file(file.filename):
        filename = secure_filename(file.filename)
        file.save(os.path.join(path, filename))
        return True
    else:
        return False

def delete_user_upload(user,filename):
    path = "{}/{}/apps/".format(get_users_dir(), user['username'])
    try:
        filename = secure_filename(filename)
        os.remove(os.path.join(path,filename))
        return True      
    except:
        print("couldn't delete file")
        return False

def get_all_users():
    users = list()
    for user in os.listdir(get_users_dir()):
        users.append(get_user(user))
        
    return users

def get_user_render_port(username):
    base_port = current_app.config.get('PIXLET_RENDER_PORT1') or 5100
    users = get_all_users()
    for i in range(len(users)):
         if users[i]['username'] == username:
            print(f"got port {i} for {username}")
            return base_port+i

def get_is_app_schedule_active(app):
    # Check if the app should be displayed based on start and end times and active days
    current_time = datetime.now().time()
    current_day = datetime.now().strftime("%A").lower()
    start_time_str = app.get("start_time", "00:00") or "00:00"
    end_time_str = app.get("end_time", "23:59") or "23:59"
    start_time = datetime.strptime(start_time_str, "%H:%M").time()
    end_time = datetime.strptime(end_time_str, "%H:%M").time()
    active_days = app.get(
        "days",
        ["monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"],
    )
    if not active_days:
        active_days = [
            "monday",
            "tuesday",
            "wednesday",
            "thursday",
            "friday",
            "saturday",
            "sunday",
        ]

    schedule_active = False
    if (
        (start_time <= current_time <= end_time)
        or (
            start_time > end_time
            and (current_time >= start_time or current_time <= end_time)
        )
    ) and current_day in active_days:
        schedule_active = True

    return schedule_active

def get_device_by_name(user,name):
    for device_id, device in user.get("devices", {}).items():
        if device.get("name") == name:
            return device
    return None

def get_user_by_device_id(device_id):
    for user in get_all_users():
        if 'devices' in user and device_id in user['devices']:
            return user

def generate_firmware(device_id,url,ap,pw):
    # Usage
    file_path = "firmware/fw.bin"
    new_path = file_path.replace("fw", f"tronbyt_{device_id[0:4]}")
    shutil.copy(file_path, new_path)

    # Replace this with the string to be replaced

    # new values should be the first three areuments passed to script
    # extract ssid, password and url from command-line arguments
    # substitutions = sys.argv[1:4]
    # dict = {
    #     "XplaceholderWIFISSID": ap,
    #     "XplaceholderWIFIPASSWORD": pw,
    #     "XplaceholderREMOTEURL___________________________________________________________________" : url,
    # }
    dict = {
        "XplaceholderWIFISSID________________________________": ap,
        "XplaceholderWIFIPASSWORD____________________________": pw,
        "XplaceholderREMOTEURL_________________________________________________________________________________________": url,
    }
    bytes_written = None
    with open(new_path, "r+b") as f:
        # Read the binary file into memory
        content = f.read()

        for old_string, new_string in dict.items():
            # Ensure the new string is not longer than the original
            if len(new_string) > len(old_string):
                return {"error" : "Replacement string cannot be longer than the original string."}

            # Find the position of the old string
            position = content.find(old_string.encode("ascii") + b"\x00")
            if position == -1:
                return {"error" : f"String '{old_string}' not found in the binary."}

            # Create the new string, null-terminated, and padded to match the original length
            padded_new_string = new_string + '\x00'
            padded_new_string = padded_new_string.ljust(len(old_string) + 1, '\x00')  # Add padding if needed

            # Replace the string
            f.seek(position)
            bytes_written = f.write(padded_new_string.encode("ascii"))
    if bytes_written:
        # run the correct checksum/hash script
        result = subprocess.run(
            ["python3", "/app/firmware/correct_firmware_esptool.py", f"/app/{new_path}"],
            capture_output=True,
            text=True
        )
        print(result.stdout)
        return {'file_path' : new_path}
    else:
        return {'error' : "no bytes written"}
