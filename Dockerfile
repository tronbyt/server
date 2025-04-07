# syntax=docker/dockerfile:1.5

# Ignore hadolint findings about version pinning
# hadolint global ignore=DL3007,DL3008,DL3013
FROM ghcr.io/tronbyt/pixlet:latest AS pixlet

# build runtime image
FROM debian:trixie-slim AS runtime

# 8000 for main app
EXPOSE 8000

ENV PYTHONUNBUFFERED=1 \
    LIBPIXLET_PATH=/usr/lib/libpixlet.so

# Create a non-root user
RUN groupadd -r -g 1000 tronbyt && useradd -r -u 1000 -g tronbyt tronbyt

WORKDIR /app

# copy pixlet library and python dependencies
COPY --from=pixlet --chmod=755 /lib/libpixlet.so /usr/lib/libpixlet.so

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates \
    esptool \
    git \
    gunicorn \
    libsharpyuv0 \
    libwebp7 \
    libwebpdemux2 \
    libwebpmux3 \
    python3 \
    python3-dotenv \
    python3-flask \
    python3-flask-babel \
    python3-pip \
    python3-requests \
    python3-tzlocal \
    python3-yaml \
    tzdata \
    tzdata-legacy && \
    pip3 install --no-cache-dir --root-user-action=ignore --break-system-packages flask-sock && \
    rm -rf /root/.cache/pip && \
    apt-get -y purge python3-pip && \
    apt-get -y autoremove && \
    rm -rf /var/lib/apt/lists/* /usr/lib/python3/dist-packages/pip /usr/bin/pip3
COPY . /app
RUN pybabel compile -d tronbyt_server/translations

# Create the directories for dynamic content ahead of time so that they are
# owned by the non-root user (newly created named volumes are owned by root,
# if their target doesn't exist).
RUN touch /app/system-apps.json && \
    mkdir -p /app/system-apps /app/tronbyt_server/static/apps /app/tronbyt_server/webp /app/users && \
    chown -R tronbyt:tronbyt /app/system-apps.json /app/system-apps /app/tronbyt_server/static/apps /app/tronbyt_server/webp /app/users && \
    chmod -R 755 /app/system-apps /app/tronbyt_server/static/apps /app/tronbyt_server/webp /app/users

# Set the user to non-root (disabled for a while to support legacy setups which ran as root)
#USER tronbyt

# start the app
ENTRYPOINT ["./run"]
