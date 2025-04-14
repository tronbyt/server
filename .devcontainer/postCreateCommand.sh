#!/bin/bash

pip3 install --user -r requirements.txt pytest
mypy --install-types --non-interactive --ignore-missing-imports --exclude system-apps .

PIXLET_VERSION=v0.42.0
curl -LO "https://github.com/tronbyt/pixlet/releases/download/${PIXLET_VERSION}/pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
sudo tar -C /usr/local/bin -xvf "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
sudo mv /usr/local/bin/libpixlet.so /usr/lib/libpixlet.so
rm "pixlet_${PIXLET_VERSION}_linux_amd64.tar.gz"
