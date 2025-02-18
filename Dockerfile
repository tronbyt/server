FROM ghcr.io/tavdog/pixlet:latest AS pixlet-builder

# build runtime image
FROM debian:bookworm-slim AS runtime

# 8000 for main app, 5100, 5101 for pixlet serve iframe
EXPOSE 8000 5100 5101

ENV PYTHONUNBUFFERED=1

WORKDIR /app

# copy pixlet binary and python dependencies
COPY --from=pixlet /bin/pixlet /pixlet/pixlet
COPY --from=pixlet --chmod=755 /lib/libpixlet.so /usr/lib/libpixlet.so

RUN echo deb http://deb.debian.org/debian bookworm-backports main > /etc/apt/sources.list.d/bookworm-backports.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        python3 python3-flask gunicorn python3-dotenv python3-requests python3-websocket python3-yaml \
        libwebp7/bookworm-backports libwebpmux3/bookworm-backports libwebpdemux2/bookworm-backports \
        procps git tzdata ca-certificates python3-pip && \
    pip3 install esptool --break-system-packages && \
    apt-get purge -y python3-pip && \
    rm -rf /var/lib/apt/lists/*

COPY . /app

# start the app
CMD ["./run"]
