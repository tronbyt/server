#!/bin/bash

# Exit on any error
set -e

echo "================================================"
echo "Raspberry Pi Tronbyt Server Setup Script"
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

# Wait for container to be ready (check for HTTP 200 or 303)
echo "Waiting for server to be ready..."
MAX_RETRIES=60
RETRY_COUNT=0
while [ $RETRY_COUNT -lt $MAX_RETRIES ]; do
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8000 2>/dev/null || echo "000")
    if [ "$HTTP_CODE" = "200" ] || [ "$HTTP_CODE" = "303" ]; then
        echo -e "\rServer is ready!                               "
        break
    fi
    printf "\rWaiting for server... (attempt %d/%d)" $((RETRY_COUNT + 1)) $MAX_RETRIES
    sleep 2
    RETRY_COUNT=$((RETRY_COUNT + 1))
done

if [ $RETRY_COUNT -eq $MAX_RETRIES ]; then
    echo -e "\rWarning: Server did not become ready within 2 minutes"
    echo ""
    echo "Docker Compose logs:"
    echo "================================================"
    sudo docker compose logs
    echo "================================================"
    echo ""
    echo "ERROR: The server appears to have failed to start."
    echo ""
    echo "This may be due to:"
    echo "  - The system-apps/firmware download taking too long"
    echo "  - The system-apps/firmware download failing"
    echo ""
    echo "Suggestions:"
    echo "  - Try accessing the server later at: http://$(hostname).local:8000"
    echo "  - Or try running this setup script again"
    echo ""
    exit 1
fi

# Get the Raspberry Pi's IP address
PI_IP=$(hostname -I | awk '{print $1}')

echo ""
echo "================================================"
echo "Setup complete!"
echo "================================================"
echo "Tronbyt container is now running."
echo "You can check the servers logs by running: sudo docker compose logs"
echo "You can check the status with: sudo docker compose ps"
echo "You can upgrade the container with: sudo docker compose pull ; sudo docker compose up -d"
echo ""
echo "Access your server at: http://$PI_IP:8000"
echo "                   or: http://$(hostname).local:8000"
echo ""
echo "Note: You may need to log out and back in for docker group"
echo "      permissions to take effect (to run docker without sudo)"
echo "================================================"
