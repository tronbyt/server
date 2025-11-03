#!/usr/bin/env python3
import os
import sys
from typing import Any

import copy
import click
import uvicorn
import logging
from logging.config import dictConfig
from uvicorn.main import main as uvicorn_cli
from uvicorn.config import LOGGING_CONFIG
from tronbyt_server.config import get_settings
from tronbyt_server.startup import run_once
from tronbyt_server.sync import get_sync_manager

logger = logging.getLogger("tronbyt_server.run")


def main() -> None:
    """
    Run the Uvicorn server with programmatic configuration that respects CLI overrides.

    This script establishes a clear order of precedence for settings:
    1. Command-line arguments (e.g., --port 9000)
    2. Environment variables (e.g., PORT=9000)
    3. Application-specific defaults defined in this script (e.g., disable pings)
    4. Uvicorn's built-in defaults (e.g., host='127.0.0.1')
    """
    # Load settings from config.py (which handles .env files)
    settings = get_settings()

    # 1. Let Uvicorn parse CLI args and env vars to establish a baseline config.
    # This captures user intent and Uvicorn's own defaults.
    try:
        ctx = uvicorn_cli.make_context(
            info_name=sys.argv[0], args=sys.argv[1:], resilient_parsing=True
        )
    except click.exceptions.Exit as e:
        # Handle cases like --version or --help where click wants to exit.
        sys.exit(e.exit_code)

    # This is our working config, starting with everything Uvicorn has parsed.
    config = ctx.params

    # 2. Define our application-specific defaults.
    # These will only be applied if not already set by the user (via CLI/env).
    port_str = os.environ.get("TRONBYT_PORT", os.environ.get("PORT", "8000"))
    try:
        port = int(port_str)
    except ValueError:
        port = 8000

    is_production = settings.PRODUCTION == "1"

    app_defaults: dict[str, Any] = {
        "app": "tronbyt_server.main:app",
        "host": os.environ.get("TRONBYT_HOST", "::"),
        "port": port,
        "log_level": settings.LOG_LEVEL.lower(),
        "forwarded_allow_ips": "*",
        "ws_ping_interval": None,  # Our most critical default: disable pings
    }

    if is_production:
        app_defaults["workers"] = int(os.environ.get("WEB_CONCURRENCY", "2"))
    else:
        app_defaults["reload"] = True

    # 3. Intelligently merge our defaults into the config.
    # We only apply our default if the user hasn't provided the setting.
    for key, value in app_defaults.items():
        source = ctx.get_parameter_source(key)
        if source not in (
            click.core.ParameterSource.COMMANDLINE,
            click.core.ParameterSource.ENVIRONMENT,
        ):
            config[key] = value

    # Custom logging configuration
    app_log_level_str = config.get("log_level", "info").upper()
    app_log_level_num = logging.getLevelName(app_log_level_str)

    # Uvicorn's log level should never be more verbose than INFO.
    # Higher number means less verbose.
    uvicorn_log_level_num = max(app_log_level_num, logging.INFO)
    uvicorn_log_level_str = logging.getLevelName(uvicorn_log_level_num)

    log_config = copy.deepcopy(LOGGING_CONFIG)

    # Configure formatters
    log_config["formatters"]["default"]["fmt"] = (
        "%(asctime)s %(levelprefix)s [%(name)s] %(message)s"  # For tronbyt_server
    )
    log_config["formatters"]["access"]["fmt"] = (
        '%(asctime)s %(levelprefix)s %(client_addr)s - "%(request_line)s" %(status_code)s'  # For uvicorn.access
    )
    log_config["formatters"]["uvicorn_no_name"] = {
        "()": "uvicorn.logging.DefaultFormatter",
        "fmt": "%(asctime)s %(levelprefix)s %(message)s",
        "use_colors": config.get("use_colors"),
    }

    # Configure handlers
    log_config["handlers"]["default"]["level"] = app_log_level_str
    log_config["handlers"]["access"]["level"] = uvicorn_log_level_str
    log_config["handlers"]["uvicorn_error_handler"] = {
        "formatter": "uvicorn_no_name",
        "class": "logging.StreamHandler",
        "stream": "ext://sys.stderr",
        "level": uvicorn_log_level_str,
    }

    # Configure loggers
    log_config["loggers"][""] = {
        "handlers": ["default"],
        "level": app_log_level_str,
    }
    log_config["loggers"]["uvicorn"] = {
        "handlers": ["uvicorn_error_handler"],
        "level": uvicorn_log_level_str,
        "propagate": False,
    }
    log_config["loggers"]["uvicorn.access"] = {
        "handlers": ["access"],
        "level": uvicorn_log_level_str,
        "propagate": False,
    }
    log_config["loggers"]["uvicorn.error"] = {
        "handlers": ["uvicorn_error_handler"],
        "level": uvicorn_log_level_str,
        "propagate": False,
    }

    # Apply the logging configuration immediately
    dictConfig(log_config)

    # Run startup tasks that should only be executed once
    run_once()

    # The 'app' argument must be positional for uvicorn.run()
    app = config.pop("app")
    config["log_config"] = log_config

    # Announce server startup using the final, merged configuration
    startup_message = "Starting server"

    if config.get("reload"):
        startup_message += " with auto-reload"
    if config.get("workers"):
        startup_message += f" with {config['workers']} workers"

    logger.info(startup_message)

    # The sync manager needs to be initialized in the parent process and shut down
    # gracefully. Using a context manager is the cleanest way to ensure this.
    with get_sync_manager():
        uvicorn.run(app, **config)


if __name__ == "__main__":
    main()
