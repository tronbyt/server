#!/bin/bash

go mod download

PIXLET_VERSION=v0.49.7
curl -LO "https://github.com/tronbyt/pixlet/releases/download/${PIXLET_VERSION}/pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
sudo tar -C /usr/local/bin -xvf "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
rm "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
