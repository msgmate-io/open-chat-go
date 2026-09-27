#!/bin/bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

if command -v openchat-integrations >/dev/null 2>&1; then
  BUILD_TOOLS="openchat-integrations"
else
  BUILD_TOOLS="$(bash "$REPO_ROOT/development/scripts/build_tools.sh")"
fi

INTEGRATION_PROFILE="${INTEGRATION_PROFILE:-}"

# Materialize the integration checkouts and link their frontend pages into the
# aggregator before building. Integration pages live in the (possibly private)
# integration repositories and are symlinked in only when the integration is
# present, so per-integration React code stays private.
echo "Linking integration frontend pages..."
"$BUILD_TOOLS" sync ${INTEGRATION_PROFILE:+--profile "$INTEGRATION_PROFILE"}
"$BUILD_TOOLS" frontend ${INTEGRATION_PROFILE:+--profile "$INTEGRATION_PROFILE"}

echo "Building frontend statically..."

# Clean up previous build
rm -rf backend/frontend/
rm -rf backend/server/frontend/
rm -rf frontend/dist/

# Navigate to frontend directory
cd frontend/

# Install dependencies
echo "Installing frontend dependencies..."
npm install

# Build the frontend using Vike
echo "Building frontend with Vike..."
npm run build

# Generate Go routes from the built files
echo "Generating Go routes..."
./generate_golang_routes.sh

cd "$REPO_ROOT"

# Export integration-owned frontend pages into integration assets. The mapping
# is driven by integrations.yaml (see `frontend.pages`) instead of a hardcoded
# shell script.
echo "Exporting integration frontend pages..."
"$BUILD_TOOLS" export ${INTEGRATION_PROFILE:+--profile "$INTEGRATION_PROFILE"} --dist-dir "$REPO_ROOT/frontend/dist/client"

# Copy the built frontend to backend directory
echo "Copying built frontend to backend..."
mkdir -p backend/server/frontend/
cp -r frontend/dist/client/* backend/server/frontend/

# Copy the routes.json file to backend
echo "Copying routes.json to backend..."
cp frontend/routes.json backend/server/routes.json

echo "Frontend build complete! Built files are in backend/server/frontend/"
echo "Routes file is in backend/server/routes.json"
