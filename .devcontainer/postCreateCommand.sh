#!/bin/bash

pipx install pdm
pdm sync -d

PIXLET_VERSION=v0.49.1
curl -LO "https://github.com/tronbyt/pixlet/releases/download/${PIXLET_VERSION}/pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
sudo tar -C /usr/local/bin -xvf "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
sudo mv /usr/local/bin/libpixlet.so /usr/lib/libpixlet.so
rm "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
