FROM golang:1.23 AS builder

ENV PIXLET_REPO=https://github.com/tavdog/pixlet

# build pixlet
RUN apt-get update && apt-get install --no-install-recommends npm libwebp-dev -y \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /
RUN git clone --depth 1 $PIXLET_REPO /pixlet
WORKDIR /pixlet
RUN npm install && npm run build && make build

FROM python:3.13-slim AS runtime

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

RUN apt-get update && apt-get install --no-install-recommends -y procps libwebp7 libwebpdemux2 libwebpmux3 git \
 && apt-get clean \
 && rm -rf /var/lib/apt/lists/*

# Copy the built pixlet binary from the builder stage
COPY --from=builder /pixlet/pixlet /pixlet/pixlet

# Set up the environment for the Flask app
WORKDIR /app
COPY . /app
RUN pip install --no-cache-dir -r requirements.txt
RUN rm -f requirements.txt

# 8000 for main app, 5100,5102 for pixlet serve iframe 
EXPOSE 8000 5100 5101

# start the app
CMD ["./run"]
