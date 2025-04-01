# python3 -m gunicorn --config gunicorn.conf.py "tronbyt_server:create_app()"

bind = "0.0.0.0:8000"
loglevel = "debug"
accesslog = "-"
access_log_format = "%(h)s %(l)s %(u)s %(t)s %(r)s %(s)s %(b)s %(f)s %(a)s"
errorlog = "-"
workers = 1
threads = 4
timeout = 120
reload = False
preload_app = True
