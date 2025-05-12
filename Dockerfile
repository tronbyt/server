# syntax=docker/dockerfile:1.5

FROM debian:trixie-slim AS builder

RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends python3-pdm
ENV PDM_CHECK_UPDATE=false
COPY . /app/
WORKDIR /app
RUN pdm install --check --prod --no-editable && pdm build --no-sdist --no-wheel

# Ignore hadolint findings about version pinning
# hadolint global ignore=DL3007,DL3008,DL3013
FROM ghcr.io/tronbyt/pixlet:0.42.1 AS pixlet

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
    git \
    libsharpyuv0 \
    libwebp7 \
    libwebpdemux2 \
    libwebpmux3 \
    python3 \
    tzdata \
    tzdata-legacy && \
    rm -rf /var/lib/apt/lists/*
COPY --from=builder /app /app
ENV PATH="/app/.venv/bin:$PATH"

# Create the directories for dynamic content ahead of time so that they are
# owned by the non-root user (newly created named volumes are owned by root,
# if their target doesn't exist).
RUN mkdir -p /app/data /app/users && \
    chown -R tronbyt:tronbyt /app/data /app/users && \
    chmod -R 755 /app/data /app/users

# Set the user to non-root (disabled for a while to support legacy setups which ran as root)
#USER tronbyt

# start the app
ENTRYPOINT ["./run"]
