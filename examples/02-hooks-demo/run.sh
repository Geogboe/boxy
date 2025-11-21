#!/bin/bash
# Start the Boxy service with hooks configuration

set -e

BOXY="../../boxy"

echo "=== Starting Boxy with Hooks Demo ==="
echo ""

# Build boxy if needed
if [ ! -f "$BOXY" ]; then
    echo "Building boxy..."
    cd ../..
    go build -o boxy ./cmd/boxy
    cd examples/02-hooks-demo
    echo "Build complete"
    echo ""
fi

# Clean up old database
if [ -f "boxy.db" ]; then
    echo "Removing old database..."
    rm boxy.db
fi

echo "Starting Boxy service..."
echo "Configuration: boxy.yaml"
echo ""
echo "The service will:"
echo "  1. Create pool with min_ready=2 containers"
echo "  2. Run finalization hooks (install tools, setup dirs)"
echo "  3. Wait for resource requests"
echo ""
echo "Press Ctrl+C to stop"
echo ""

$BOXY serve --config boxy.yaml
