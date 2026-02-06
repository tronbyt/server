#!/bin/bash

# Exit on any error
set -e

echo "================================================"
echo "Raspberry Pi Server Setup Script"
echo "================================================"
echo ""

# Update package list
echo "[1/6] Updating package list..."
sudo apt-get update

# Install git if not already installed
echo "[2/6] Installing git..."
sudo apt-get install -y git

# Install Docker if not already installed
echo "[3/6] Installing Docker..."
if ! command -v docker &> /dev/null; then
    # Install Docker using the official convenience script
    curl -fsSL https://get.docker.com -o get-docker.sh
    sudo sh get-docker.sh
    rm get-docker.sh

    # Add current user to docker group to run docker without sudo
    sudo usermod -aG docker $USER
    echo "Docker installed successfully!"
else
    echo "Docker is already installed"
fi

# Install Docker Compose (plugin version)
echo "[4/6] Installing Docker Compose..."
sudo apt-get install -y docker-compose-plugin

# Clone the repository
echo "[5/6] Cloning repository..."
REPO_DIR="$HOME/tronbyt-server"
if [ -d "$REPO_DIR" ]; then
    echo "Repository directory already exists. Pulling latest changes..."
    cd "$REPO_DIR"
    git pull
else
    git clone https://github.com/tronbyt/server-docker-compose "$REPO_DIR"
    cd "$REPO_DIR"
fi

# Start Docker Compose
echo "[6/6] Starting Docker containers..."
sudo docker compose up -d

echo ""
echo "================================================"
echo "Setup complete!"
echo "================================================"
echo "Docker containers are now running."
echo "You can check their status with: sudo docker compose ps"
echo ""
echo "Note: You may need to log out and back in for docker group"
echo "      permissions to take effect (to run docker without sudo)"
echo "================================================"
