#!/bin/bash
set -e # Exit on error

# --- Log everything ---
exec > >(tee /var/log/startup-script.log|logger -t startup-script -s 2>/dev/console) 2>&1

echo "--- Starting GCE startup script ---"

# --- Configure Swap ---
echo "Configuring swap space..."
SWAPFILE="/swapfile"
SWAPSIZE_MB=1024

if ! swapon --show | grep -q "${SWAPFILE}"; then
  if [ ! -f "${SWAPFILE}" ]; then
    echo "Creating swap file at ${SWAPFILE}..."
    fallocate -l ${SWAPSIZE_MB}M ${SWAPFILE}
  fi

  chmod 600 ${SWAPFILE}
  mkswap ${SWAPFILE}
  swapon ${SWAPFILE}
else
  echo "Swap file ${SWAPFILE} already active."
fi

if ! grep -q "${SWAPFILE} none swap" /etc/fstab; then
  echo "Adding swap file to /etc/fstab..."
  echo "${SWAPFILE} none swap sw 0 0" >> /etc/fstab
fi

# --- Install Docker ---
echo "Installing Docker..."
apt-get update
apt-get install -y apt-transport-https ca-certificates curl gnupg lsb-release
mkdir -p /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/debian/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/debian $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

# --- Mount Persistent Disk ---
echo "Formatting and mounting persistent disk..."
DEVICE_NAME="/dev/disk/by-id/google-tronbyt-data-disk" # Use the stable disk ID
MOUNT_DIR="/mnt/disks/data"

# Only format the disk if it doesn't already have a filesystem.
# This prevents data loss on reboots.
if ! blkid -s TYPE -o value ${DEVICE_NAME} | grep -q "ext4"; then
  echo "Disk ${DEVICE_NAME} does not have an ext4 filesystem. Formatting..."

  # Check if the device is mounted anywhere and unmount it.
  MOUNT_POINT=$(lsblk -no MOUNTPOINT ${DEVICE_NAME})
  if [ -n "$MOUNT_POINT" ]; then
    echo "Device ${DEVICE_NAME} is mounted at ${MOUNT_POINT}. Unmounting before formatting."
    umount ${MOUNT_POINT}
  fi

  # Force filesystem creation.
  mkfs.ext4 -F -m 0 -E lazy_itable_init=0,lazy_journal_init=0,discard ${DEVICE_NAME}
fi

# Mount the disk if it's not already mounted at the target directory.
if ! mountpoint -q ${MOUNT_DIR}; then
  echo "Mounting ${DEVICE_NAME} to ${MOUNT_DIR}..."
  mkdir -p ${MOUNT_DIR}
  mount -o discard,defaults ${DEVICE_NAME} ${MOUNT_DIR}
  chmod a+w ${MOUNT_DIR}
fi

# Add to fstab for auto-mounting on reboot, ensuring not to add duplicate entries.
if ! grep -q "${DEVICE_NAME} ${MOUNT_DIR}" /etc/fstab; then
  echo "Adding ${DEVICE_NAME} to /etc/fstab..."
  echo "${DEVICE_NAME} ${MOUNT_DIR} ext4 defaults 1 1" >> /etc/fstab
fi

# --- Prepare Application Directory ---
echo "Setting up application directory..."
APP_DIR="$MOUNT_DIR/tronbyt-server"
mkdir -p $APP_DIR
cd $APP_DIR

# --- Download Docker Compose file ---
echo "Downloading docker-compose.redis.yaml..."
curl -o docker-compose.redis.yaml https://raw.githubusercontent.com/tronbyt/tronbyt-server/main/docker-compose.redis.yaml

# --- Create Docker Volume Directories ---
echo "Creating directories for Docker volumes..."
mkdir -p ./data
mkdir -p ./redis_data # Renamed to avoid conflict with the 'data' directory

# --- Set correct permissions for volume directories ---
echo "Setting ownership of volume directories to 1000:1000..."
chown -R 1000:1000 ./data ./redis_data

# --- Create .env file ---
# These variables are passed in as metadata from Terraform
echo "Creating .env file..."

cat <<EOF > .env
REDIS_URL=redis://redis:6379
EOF

# --- Update docker-compose.yaml to use local paths ---
# The default docker-compose.yaml uses named volumes. We need to map them to our persistent disk paths.
echo "Updating docker-compose file..."
sed -i 's|data:/app/data|./data:/app/data|' docker-compose.redis.yaml
sed -i 's|redis:/data|./redis_data:/data|' docker-compose.redis.yaml


# --- Run Docker Compose ---
echo "Starting application with Docker Compose..."
# Use the redis compose file
docker compose -f docker-compose.redis.yaml up -d

echo "--- GCE startup script finished ---"
