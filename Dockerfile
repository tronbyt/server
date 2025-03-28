# Ignore hadolint findings about version pinning
# hadolint global ignore=DL3007,DL3008,DL3013
FROM ghcr.io/tronbyt/pixlet:latest AS pixlet

# build runtime image
FROM debian:trixie-slim AS runtime

# 8000 for main app
EXPOSE 8000

ENV PYTHONUNBUFFERED=1
ENV LIBPIXLET_PATH=/usr/lib/libpixlet.so

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
        python3-requests \
        python3-tzlocal \
        python3-yaml \
        tzdata \
        tzdata-legacy && \
    rm -rf /var/lib/apt/lists/*

COPY . /app
RUN pybabel compile -d tronbyt_server/translations

# start the app
CMD ["./run"]
