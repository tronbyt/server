#!/usr/bin/env python3
import os
import sys
from typing import Any

import click
import uvicorn
from tronbyt_server.startup import run_once
from uvicorn.main import main as uvicorn_cli


def main() -> None:
    """
    Run the Uvicorn server with programmatic configuration that respects CLI overrides.

    This script establishes a clear order of precedence for settings:
    1. Command-line arguments (e.g., --port 9000)
    2. Environment variables (e.g., PORT=9000)
    3. Application-specific defaults defined in this script (e.g., disable pings)
    4. Uvicorn's built-in defaults (e.g., host='127.0.0.1')
    """
    # Run startup tasks that should only be executed once
    run_once()

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

    is_production = os.environ.get("PRODUCTION", "1") == "1"

    app_defaults: dict[str, Any] = {
        "app": "tronbyt_server.main:app",
        "host": os.environ.get("TRONBYT_HOST", "::"),
        "port": port,
        "log_level": os.environ.get("LOG_LEVEL", "info").lower(),
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

    # The 'app' argument must be positional for uvicorn.run()
    app = config.pop("app")

    # Announce server startup using the final, merged configuration
    startup_message = f"Starting server on {config['host']}:{config['port']}"

    if config.get("reload"):
        startup_message += " with auto-reload"
    elif config.get("workers"):
        startup_message += f" with {config['workers']} workers"

    print(startup_message)

    uvicorn.run(app, **config)


if __name__ == "__main__":
    main()
