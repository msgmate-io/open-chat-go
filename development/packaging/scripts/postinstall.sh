#!/bin/sh
set -e

# Idempotently ensure the service account and the state/config directories
# exist. dpkg normally unpacks the conffile already owned by open-chat, but we
# repair ownership defensively in case the archive was unpacked before the user
# existed.

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

install -d -m 0750 -o root -g open-chat /etc/open-chat
install -d -m 0750 -o open-chat -g open-chat /var/lib/open-chat

if [ -f /etc/open-chat/open-chat.json ]; then
    chown open-chat:open-chat /etc/open-chat/open-chat.json 2>/dev/null || true
    chmod 0600 /etc/open-chat/open-chat.json 2>/dev/null || true
fi

# Apply the sysusers/tmpfiles definitions on systemd hosts (no-op elsewhere).
if command -v systemd-sysusers >/dev/null 2>&1; then
    systemd-sysusers /usr/lib/sysusers.d/open-chat.conf >/dev/null 2>&1 || true
fi
if command -v systemd-tmpfiles >/dev/null 2>&1; then
    systemd-tmpfiles --create /usr/lib/tmpfiles.d/open-chat.conf >/dev/null 2>&1 || true
fi

if [ "$1" = "configure" ]; then
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload >/dev/null 2>&1 || true
    fi

    action=start
    if command -v deb-systemd-helper >/dev/null 2>&1; then
        deb-systemd-helper unmask open-chat.service >/dev/null 2>&1 || true
        if deb-systemd-helper --quiet was-enabled open-chat.service; then
            action=restart
        else
            deb-systemd-helper enable open-chat.service >/dev/null 2>&1 || true
        fi
    fi

    # deb-systemd-invoke honours policy-rc.d (container/image builds); fall
    # back to systemctl on non-Debian hosts.
    if command -v deb-systemd-invoke >/dev/null 2>&1; then
        deb-systemd-invoke "$action" open-chat.service >/dev/null 2>&1 || true
    elif command -v systemctl >/dev/null 2>&1; then
        systemctl "$action" open-chat.service >/dev/null 2>&1 || true
    fi
fi

exit 0
