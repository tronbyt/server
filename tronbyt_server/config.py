import yaml
import os

def load_config():
    # The instance folder is at the root of the project.
    # The current file is in tronbyt_server/.
    # So we need to go up one level to the project root.
    project_root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    config_path = os.path.join(project_root, 'instance', 'config.yaml')

    # Check if the file exists before trying to open it.
    if not os.path.exists(config_path):
        # In a test environment, the instance folder might not exist.
        # We can create a dummy config for now.
        return {"ENABLE_USER_REGISTRATION": "1"}

    with open(config_path, "r") as f:
        return yaml.safe_load(f)

config = load_config()
