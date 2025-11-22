#!/bin/bash
# Start Boxy service with simple Docker pool

set -e

# Build boxy if not already built
if [ ! -f "../../boxy" ]; then
    echo "Building Boxy..."
    cd ../..
    go build -o boxy ./cmd/boxy
    cd -
fi

# Clean up old database
rm -f boxy.db

echo "Starting Boxy service with simple Docker pool..."
echo "Press Ctrl+C to stop"
echo ""

../../boxy serve --config boxy.yaml --log-level info
