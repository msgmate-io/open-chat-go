#!/bin/bash

echo "Building frontend statically..."

# Clean up previous build
rm -rf backend/frontend/
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

# Copy the built frontend to backend directory
echo "Copying built frontend to backend..."
mkdir -p ../backend/frontend/
cp -r dist/client/* ../backend/server/frontend/

# Copy the routes.json file to backend
echo "Copying routes.json to backend..."
cp routes.json ../backend/server/routes.json

# Return to root directory
cd ../

echo "Frontend build complete! Built files are in backend/frontend/"
echo "Routes file is in backend/server/routes.json"