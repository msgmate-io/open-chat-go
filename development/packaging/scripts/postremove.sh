#!/bin/sh
set -e

if command -v deb-systemd-helper >/dev/null 2>&1; then
    deb-systemd-helper purge open-chat.service >/dev/null 2>&1 || true
    deb-systemd-helper unmask open-chat.service >/dev/null 2>&1 || true
fi

if command -v systemctl >/dev/null 2>&1; then
    systemctl daemon-reload >/dev/null 2>&1 || true
fi

# `apt remove` keeps the database and configuration; `apt purge` removes the
# state directory. The conffile itself is handled by dpkg.
if [ "$1" = "purge" ]; then
    rm -rf /var/lib/open-chat
fi

exit 0
