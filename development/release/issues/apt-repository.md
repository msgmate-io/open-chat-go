# [Release] APT repository & Debian packages for open-chat

## Context
We want `apt install open-chat` for Debian/Ubuntu. The binary is self-contained and the
CLI already supports service install/uninstall/status (kardianos/service).

## Goal
Research, plan and prepare a Debian package + apt repository (optionally rpm), then implement.

## Research questions
- Tooling: nfpm (deb+rpm+apk from one yaml) vs GoReleaser `nfpms` vs classic debian/ +
  debhelper. Recommend and justify.
- Layout: /usr/bin/open-chat, systemd unit, /etc/open-chat/open-chat.json (conffile 0600),
  /var/lib/open-chat state dir, dedicated `open-chat` system user/group, tmpfiles.
- Maintainer scripts: create user/group, preserve config, enable/start service; use the
  built-in `open-chat install` or ship a native unit?
- Repo hosting + signing: GitHub Pages (aptly/reprepro) vs Cloudsmith/Packagecloud vs
  Releases + signed Release/Packages; GPG key management in CI.
- Static linking: current binaries are glibc-dynamic — add CGO_ENABLED=0 before packaging.
- Version consistency: tag (open-chat-0.0.610) vs asset version (0.0.611) skew must be fixed.

## Deliverables
- Packaging config + CI job building .deb and publishing a signed apt repo.
- Install docs (`apt install open-chat`).

## Acceptance criteria
- `apt-get install open-chat` works on Ubuntu 22.04/24.04 and Debian 12.
- Package creates service user, installs unit, starts on :1984, data under /var/lib/open-chat.
- `open-chat status` reports running.
