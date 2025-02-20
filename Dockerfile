# Ignore hadolint findings about version pinning
# hadolint global ignore=DL3007,DL3008,DL3013
FROM ghcr.io/tavdog/pixlet:latest AS pixlet

# build runtime image
FROM debian:trixie-slim AS runtime

# 8000 for main app, 5100, 5101 for pixlet serve iframe
EXPOSE 8000 5100 5101

ENV PYTHONUNBUFFERED=1

WORKDIR /app

# copy pixlet binary and python dependencies
COPY --from=pixlet /bin/pixlet /pixlet/pixlet
COPY --from=pixlet --chmod=755 /lib/libpixlet.so /usr/lib/libpixlet.so

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        python3 python3-flask gunicorn python3-dotenv python3-requests python3-websocket python3-yaml python3-flask-babel \
        libwebp7 libwebpmux3 libwebpdemux2 libsharpyuv0 \
        procps git tzdata ca-certificates python3-pip && \
    pip3 install --no-cache-dir --root-user-action=ignore esptool --break-system-packages && \
    apt-get purge -y python3-pip && \
    rm -rf /var/lib/apt/lists/*

COPY . /app
RUN pybabel compile -d tronbyt_server/translations

# start the app
CMD ["./run"]
