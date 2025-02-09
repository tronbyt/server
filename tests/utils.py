import os,json

config_path =  "tests/users/testuser/testuser_debug.json"
uploads_path = "tests/users/testuser/apps"
def get_testuser_config_string():
    with open(config_path) as file:
        data = file.read()
        return data

          
def get_test_device_id():
    config = json.loads(get_testuser_config_string())
    return list(config['devices'].keys())[0]

def get_user_uploads_list():
    return os.listdir(uploads_path)

def get_test_app_id():
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    return list(config['devices'][device_id]['apps'].keys())[0]

def get_test_app_dict():
    config = json.loads(get_testuser_config_string())
    device_id = get_test_device_id()
    return list(config['devices'][device_id]['apps'].values())[0]

     

