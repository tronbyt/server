from fastapi.templating import Jinja2Templates
from fastapi import Request
from tronbyt_server.flash import get_flashed_messages
from jinja2 import contextfunction

templates = Jinja2Templates(directory="tronbyt_server/templates")

def _(text):
    """A dummy function for i18n."""
    return text

@contextfunction
def url_for(context: dict, name: str, **path_params) -> str:
    request: Request = context["request"]
    return request.url_for(name, **path_params)

templates.env.globals["get_flashed_messages"] = get_flashed_messages
templates.env.globals["_"] = _
templates.env.globals["url_for"] = url_for

def- template_response(template_name: str, context: dict):
-    request = context.get("request")
-    if request:
-        context["get_flashed_messages"] = lambda: get_flashed_messages(request)
-    return templates.TemplateResponse(template_name, context)
+
+
+def flash(request: Request, message: str, category: str = "primary"):
+    if "_messages" not in request.session:
+        request.session["_messages"] = []
+    request.session["_messages"].append({"message": message, "category": category})
