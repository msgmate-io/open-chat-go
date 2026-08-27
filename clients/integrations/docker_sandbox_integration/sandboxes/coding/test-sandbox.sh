#!/bin/bash
set -euo pipefail

# Clean up any existing containers/images
cleanup() {
    echo "Cleaning up test resources..."
    if [ -n "${CONTAINER_ID:-}" ]; then
        docker stop "$CONTAINER_ID" > /dev/null
        docker rm "$CONTAINER_ID" > /dev/null
    fi
    docker compose down -v --rmi none > /dev/null 2>&1
}

# Trap to ensure cleanup even if script fails
trap cleanup EXIT INT TERM

echo "=== Starting development sandbox test ==="

# Build the Docker image
echo "1/6: Building Docker image..."
docker compose build dev-sandbox

# Start detached container
echo "2/6: Starting test container..."
CONTAINER_ID=$(docker compose run -d dev-sandbox bash -c "sleep 3600")

# Wait for container to initialize
sleep 3

# Verify all required tools are installed
echo "3/6: Validating installed tools..."

TOOLS=("git --version" "gh --version" "opencode --version" "vim --version | head -n1" "bash --version | head -n1" "curl --version | head -n1" "jq --version" "python3 --version" "make --version | head -n1" "docker --version | head -n1" "docker compose version | head -n1")

for tool_cmd in "${TOOLS[@]}"; do
    tool_name=$(echo "$tool_cmd" | cut -d' ' -f1)
    output=$(docker exec "$CONTAINER_ID" bash -c "$tool_cmd" 2>&1)
    if [ $? -eq 0 ]; then
        echo "   ✓ $tool_name: $output"
    else
        echo "   ✗ $tool_name failed: $output"
        exit 1
    fi
done

# Test docker-in-docker: daemon, image build, and compose
echo "4/6: Testing docker-in-docker..."
docker exec "$CONTAINER_ID" bash -c "docker info --format '{{.ServerVersion}}' > /dev/null"
server_version=$(docker exec "$CONTAINER_ID" bash -c "docker info --format '{{.ServerVersion}}'")
echo "   ✓ Nested Docker daemon running (server version: $server_version)"

docker exec "$CONTAINER_ID" bash -c "mkdir -p /tmp/dind-test && printf 'FROM alpine:3.20\nCMD [\"echo\", \"dind-build-works\"]\n' > /tmp/dind-test/Dockerfile"
docker exec "$CONTAINER_ID" bash -c "cd /tmp/dind-test && docker build -q -t dind-test . > /dev/null"
docker exec "$CONTAINER_ID" bash -c "docker run --rm dind-test" | grep -q "dind-build-works"
echo "   ✓ Nested docker build + run works"

compose_version=$(docker exec "$CONTAINER_ID" bash -c "docker compose version --short")
echo "   ✓ Nested docker compose available (v$compose_version)"

# Test workspace mount
echo "5/6: Testing workspace volume mount..."
test_file_content="Hello from test file!"
test_file_path="/workspace/test-sandbox-$(date +%s).txt"
docker exec "$CONTAINER_ID" bash -c "echo '$test_file_content' > $test_file_path"
mounted_content=$(cat "$(dirname "$0")/$(basename "$test_file_path")" 2>/dev/null || true)
if [ "$mounted_content" = "$test_file_content" ]; then
    echo "   ✓ Workspace volume mounted correctly"
    # Clean up test file
    rm -f "$(dirname "$0")/$(basename "$test_file_path")"
else
    echo "   ✗ Workspace volume mount failed"
    exit 1
fi

echo "6/6: Summary"
echo "=== All tests passed successfully! ==="
echo "
To use the sandbox:
  docker compose run --rm dev-sandbox

To persist configs: volumes are automatically created for opencode, gh and docker settings"