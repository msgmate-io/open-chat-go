#!/bin/bash

set -e  # Exit on any error

# Parse command line arguments
INSTALL=false
FEDERATION=true
FRONTEND=true
for arg in "$@"; do
    case $arg in
        -i|--install)
            INSTALL=true
            shift
            ;;
        --no-federation)
            FEDERATION=false
            shift
            ;;
        --no-frontend)
            FRONTEND=false
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [OPTIONS]"
            echo "Options:"
            echo "  -i, --install        Install the backend after successful build"
            echo "  --no-federation      Build without federation support (federation package will be excluded)"
            echo "  --no-frontend        Skip frontend build (useful for backend-only builds)"
            echo "  -h, --help           Show this help message"
            echo ""
            echo "Environment Variables for Build-time Defaults:"
            echo "  BUILD_DEFAULT_BOT                    Default bot credentials (format: username:password)"
            echo "  BUILD_DEFAULT_NETWORK_CREDENTIALS    Default network credentials (format: username:password)"
            echo "  BUILD_NETWORK_BOOTSTRAP_PEERS        Bootstrap peers (comma-separated base64 encoded node info)"
            echo ""
            echo "Examples:"
            echo "  $0                                    # Build with federation and frontend (default)"
            echo "  $0 --no-federation                   # Build without federation"
            echo "  $0 --no-frontend                     # Build backend only (skip frontend)"
            echo "  $0 --no-federation --no-frontend     # Build backend only without federation"
            echo "  BUILD_DEFAULT_BOT=mybot:mypass $0    # Build with custom bot credentials"
            exit 0
            ;;
        *)
            echo "Unknown option: $arg"
            echo "Use -h or --help for usage information"
            exit 1
            ;;
    esac
done

# Get the directory where this script is located
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Get the repo root (parent of backend directory)
REPO_ROOT="$(dirname "$SCRIPT_DIR")"

echo "Starting full build process..."
echo "Script directory: $SCRIPT_DIR"
echo "Repo root: $REPO_ROOT"
if [ "$INSTALL" = true ]; then
    echo "Install mode: Will run 'sudo ./backend install' after successful build"
fi
if [ "$FRONTEND" = false ]; then
    echo "Frontend build: Skipped (--no-frontend flag)"
fi

# Change to repo root
cd "$REPO_ROOT"

# Step 1: Build the static frontend (if not skipped)
if [ "$FRONTEND" = true ]; then
    echo "Step 1: Building static frontend..."
    ./_scripts/build_static_frontend.sh
else
    echo "Step 1: Skipping frontend build (--no-frontend flag)"
fi

# Step 2: Increment minor version in metrics handler
if [ "$FRONTEND" = true ]; then
    echo "Step 2: Incrementing version in metrics handler..."
else
    echo "Step 1: Incrementing version in metrics handler..."
fi

# Read current version
CURRENT_VERSION=$(grep 'var VERSION =' backend/api/metrics/handler.go | sed 's/.*"\(.*\)".*/\1/')
echo "Current version: $CURRENT_VERSION"

# Parse version (assuming format: major.minor.patch)
IFS='.' read -ra VERSION_PARTS <<< "$CURRENT_VERSION"
MAJOR=${VERSION_PARTS[0]}
MINOR=${VERSION_PARTS[1]}
PATCH=${VERSION_PARTS[2]}

# Increment patch version (the last number)
NEW_PATCH=$((PATCH + 1))
NEW_VERSION="$MAJOR.$MINOR.$NEW_PATCH"
echo "New version: $NEW_VERSION"

TMP_FILE="$(mktemp)"
sed "s|var VERSION = \"$CURRENT_VERSION\"|var VERSION = \"$NEW_VERSION\"|" backend/api/metrics/handler.go > "$TMP_FILE" && mv "$TMP_FILE" backend/api/metrics/handler.go

echo "Version updated to: $NEW_VERSION"

# Step 3: Build the Go backend
if [ "$FRONTEND" = true ]; then
    echo "Step 3: Building Go backend..."
else
    echo "Step 2: Building Go backend..."
fi
cd backend

# Set build tags based on federation flag
BUILD_TAGS=""
if [ "$FEDERATION" = false ]; then
    BUILD_TAGS="-tags !federation"
    echo "Building without federation support"
else
    BUILD_TAGS="-tags federation"
    echo "Building with federation support"
fi

# Build ldflags for build-time defaults
LDFLAGS=""

# Add build-time defaults if environment variables are set
if [ -n "$BUILD_DEFAULT_BOT" ]; then
    LDFLAGS="$LDFLAGS -X backend/cmd.buildTimeDefaultBot=$BUILD_DEFAULT_BOT"
    echo "Setting build-time default bot: $BUILD_DEFAULT_BOT"
fi

if [ -n "$BUILD_DEFAULT_NETWORK_CREDENTIALS" ]; then
    LDFLAGS="$LDFLAGS -X backend/cmd.buildTimeDefaultNetworkCredentials=$BUILD_DEFAULT_NETWORK_CREDENTIALS"
    echo "Setting build-time default network credentials: $BUILD_DEFAULT_NETWORK_CREDENTIALS"
fi

if [ -n "$BUILD_NETWORK_BOOTSTRAP_PEERS" ]; then
    LDFLAGS="$LDFLAGS -X backend/cmd.buildTimeNetworkBootstrapPeers=$BUILD_NETWORK_BOOTSTRAP_PEERS"
    echo "Setting build-time bootstrap peers: $BUILD_NETWORK_BOOTSTRAP_PEERS"
fi

# Build with ldflags and build tags
if [ -n "$LDFLAGS" ]; then
    echo "Building with ldflags: $LDFLAGS and build tags: $BUILD_TAGS"
    go build $BUILD_TAGS -ldflags "$LDFLAGS" -o backend .
else
    echo "Building with build tags: $BUILD_TAGS (no custom ldflags)"
    go build $BUILD_TAGS -o backend .
fi

echo "Full build complete!"
if [ "$FRONTEND" = true ]; then
    echo "Frontend built and copied to backend/frontend/"
else
    echo "Frontend build skipped (--no-frontend flag)"
fi
echo "Version updated to: $NEW_VERSION"
if [ "$FEDERATION" = false ]; then
    echo "Backend binary built as: backend (without federation support)"
else
    echo "Backend binary built as: backend (with federation support)"
fi

# Step 4: Install if requested
if [ "$INSTALL" = true ]; then
    if [ "$FRONTEND" = true ]; then
        echo "Step 4: Installing backend..."
    else
        echo "Step 3: Installing backend..."
    fi
    echo "Running: sudo ./backend install"
    sudo ./backend install
    echo "Installation complete!"
else
    echo "To install the backend, run: sudo ./backend install"
fi
