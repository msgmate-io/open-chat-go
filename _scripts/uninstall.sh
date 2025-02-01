#!/bin/bash

# Configuration - make sure these match install.sh
SERVICE_NAME="open-chat"
BINARY_NAME="backend"
INSTALL_PATH="/usr/local/bin"
SERVICE_PATH="/etc/systemd/system"

# Check if script is run as root
if [ "$EUID" -ne 0 ]; then 
    echo "Please run as root (use sudo)"
    exit 1
fi

# Stop and disable the service if it exists
if systemctl is-active --quiet $SERVICE_NAME; then
    echo "Stopping service..."
    systemctl stop $SERVICE_NAME
fi

if systemctl is-enabled --quiet $SERVICE_NAME; then
    echo "Disabling service..."
    systemctl disable $SERVICE_NAME
fi

# Remove service file
echo "Removing service file..."
rm -f $SERVICE_PATH/$SERVICE_NAME.service

# Reload systemd daemon
systemctl daemon-reload

# Remove binary
echo "Removing binary..."
rm -f $INSTALL_PATH/$BINARY_NAME

echo "Uninstallation complete!" 