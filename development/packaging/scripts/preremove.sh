#!/bin/sh
set -e

# Stop the service before the binary/unit are removed. No-op on upgrade, where
# dpkg invokes this with "upgrade".

case "$1" in
    remove|deconfigure)
        if command -v deb-systemd-invoke >/dev/null 2>&1; then
            deb-systemd-invoke stop open-chat.service >/dev/null 2>&1 || true
        elif command -v systemctl >/dev/null 2>&1; then
            systemctl stop open-chat.service >/dev/null 2>&1 || true
        fi
        ;;
esac

exit 0
