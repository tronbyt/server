from fastapi.templating import Jinja2Templates
from tronbyt_server.config import config

def _(text):
    """A dummy gettext function for the templates."""
    return text

templates = Jinja2Templates(directory="tronbyt_server/templates")
templates.env.globals['_'] = _
templates.env.globals['config'] = config
