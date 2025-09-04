#!/bin/sh
set -e

# Create the service group/user before the package is unpacked so that dpkg can
# chown the packaged config to open-chat:open-chat. This is idempotent and runs
# on both initial install and upgrade.

if ! getent group open-chat >/dev/null 2>&1; then
    if command -v groupadd >/dev/null 2>&1; then
        groupadd --system open-chat || true
    elif command -v addgroup >/dev/null 2>&1; then
        addgroup -S open-chat || true
    fi
fi

if ! getent passwd open-chat >/dev/null 2>&1; then
    if command -v useradd >/dev/null 2>&1; then
        useradd --system \
            --gid open-chat \
            --home-dir /var/lib/open-chat \
            --shell /usr/sbin/nologin \
            --no-create-home \
            open-chat || true
    elif command -v adduser >/dev/null 2>&1; then
        adduser -S -D -H \
            -h /var/lib/open-chat \
            -s /usr/sbin/nologin \
            -G open-chat \
            open-chat || true
    fi
fi

exit 0
