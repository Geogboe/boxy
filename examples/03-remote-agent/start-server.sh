#!/bin/bash
# Start the Boxy server (connects to remote agent)
#
# This script can be run on any machine (typically Linux)

set -e

BOXY="../../boxy"

echo "=== Starting Boxy Server with Remote Agent ==="
echo ""

# Build boxy if needed
if [ ! -f "$BOXY" ]; then
    echo "Building boxy..."
    cd ../..
    go build -o boxy ./cmd/boxy
    cd examples/03-remote-agent
    echo "Build complete"
    echo ""
fi

# Clean up old database
if [ -f "boxy.db" ]; then
    echo "Removing old database..."
    rm boxy.db
fi

echo "Configuration: server-config.yaml"
echo ""

echo "⚠️  IMPORTANT: Update server-config.yaml with your Windows machine address!"
echo ""
echo "Current agent address:"
grep "address:" server-config.yaml | head -1
echo ""

read -p "Have you updated the agent address? (y/n) " -n 1 -r
echo ""

if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo ""
    echo "Please edit server-config.yaml and update:"
    echo "  agents[0].address: \"YOUR-WINDOWS-IP:50051\""
    echo ""
    echo "Example:"
    echo "  agents:"
    echo "    - id: windows-agent"
    echo "      address: \"192.168.1.100:50051\"  # Your Windows machine"
    echo ""
    exit 1
fi

echo ""
echo "Starting Boxy server..."
echo "The server will:"
echo "  1. Connect to remote agent (Windows machine)"
echo "  2. Register remote Hyper-V provider"
echo "  3. Start warming pools"
echo "  4. Accept sandbox requests"
echo ""
echo "Press Ctrl+C to stop"
echo ""

$BOXY serve --config server-config.yaml
