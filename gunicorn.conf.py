import os

# python3 -m gunicorn --config gunicorn.conf.py tronbyt_server.main:app

bind = "0.0.0.0:8000"
loglevel = "debug"
accesslog = "-"
access_log_format = "%(h)s %(l)s %(u)s %(t)s %(r)s %(s)s %(b)s %(f)s %(a)s"
errorlog = "-"
try:
    workers = int(os.getenv("GUNICORN_WORKERS", "2"))
except ValueError:
    workers = 2
worker_class = "uvicorn.workers.UvicornWorker"
timeout = 120
reload = False
preload_app = True
