#!/bin/bash

go mod download

PIXLET_VERSION=v0.55.1
PIXLET_ARCHIVE="pixlet_${PIXLET_VERSION}_linux-amd64.tar.gz"
curl -fLO "https://github.com/tronbyt/pixlet/releases/download/${PIXLET_VERSION}/${PIXLET_ARCHIVE}"
sudo tar -C /usr/local/bin -xvf "${PIXLET_ARCHIVE}"
rm "${PIXLET_ARCHIVE}"
