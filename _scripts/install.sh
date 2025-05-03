#!/bin/bash

# Configuration
SERVICE_NAME="open-chat"
BINARY_NAME="backend"
INSTALL_PATH="/usr/local/bin"
SERVICE_PATH="/etc/systemd/system"

# Check if script is run as root
if [ "$EUID" -ne 0 ]; then 
    echo "Please run as root (use sudo)"
    exit 1
fi

# Copy binary to installation path
echo "Installing binary to $INSTALL_PATH/$BINARY_NAME..."
cp ./backend/$BINARY_NAME $INSTALL_PATH/
chmod +x $INSTALL_PATH/$BINARY_NAME

# Create service file
echo "Creating systemd service..."
cat > $SERVICE_PATH/$SERVICE_NAME.service << EOL
[Unit]
Description=Open Chat Backend Server Service
After=network.target

[Service]
Type=simple
ExecStart=$INSTALL_PATH/$BINARY_NAME
Restart=always
RestartSec=3
User=root

[Install]
WantedBy=multi-user.target
EOL

# Reload systemd daemon
systemctl daemon-reload

# Stop existing service if it exists
if systemctl is-active --quiet $SERVICE_NAME; then
    echo "Stopping existing service..."
    systemctl stop $SERVICE_NAME
fi

# Enable and start the service
echo "Enabling and starting service..."
systemctl enable $SERVICE_NAME
systemctl start $SERVICE_NAME

echo "Installation complete! Service status:"
systemctl status $SERVICE_NAME

