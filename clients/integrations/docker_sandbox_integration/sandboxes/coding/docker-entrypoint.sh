#!/bin/bash
set -e

# Start the Docker daemon in the background (docker-in-docker)
if [ ! -e /var/run/docker.pid ]; then
    mkdir -p /var/lib/docker /var/run /var/log
    dockerd > /var/log/dockerd.log 2>&1 &
fi

# Wait until the daemon is ready
timeout=30
until docker info > /dev/null 2>&1; do
    timeout=$((timeout - 1))
    if [ "$timeout" -le 0 ]; then
        echo "ERROR: Docker daemon failed to start within 30s. See /var/log/dockerd.log" >&2
        exit 1
    fi
    sleep 1
done

exec "$@"
