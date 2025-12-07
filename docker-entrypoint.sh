#!/bin/sh
set -e

if [ "$(id -u)" = '0' ]; then
    # Set user and group id, default to 1000
    PUID=${PUID:-1000}
    PGID=${PGID:-1000}

    CURRENT_UID=$(id -u tronbyt)
    CURRENT_GID=$(id -g tronbyt)

    if [ "$CURRENT_GID" -ne "$PGID" ]; then
        groupmod -o -g "$PGID" tronbyt
    fi

    if [ "$CURRENT_UID" -ne "$PUID" ]; then
        usermod -o -u "$PUID" tronbyt
    fi

    # Take ownership of directories if needed
    if [ "$(stat -c %u /app/data)" != "$PUID" ] || [ "$(stat -c %g /app/data)" != "$PGID" ]; then
        chown -R tronbyt:tronbyt /app/data /app/users
    fi

    # Execute the command as tronbyt user
    exec su-exec tronbyt "$@"
else
    # If not running as root, just execute the command
    exec "$@"
fi
