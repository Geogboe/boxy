#!/bin/bash
set -e

echo "🚀 Quickstart: Scratch Provider"
echo "================================"
echo ""

# Use local ./boxy if present, else fallback to boxy in PATH
if [ -x "./boxy" ]; then
    BOXY_CMD="./boxy"
    echo "✓ Using local ./boxy binary"
else
    if command -v boxy &> /dev/null; then
        BOXY_CMD="boxy"
        echo "✓ Found boxy in PATH: $(which boxy)"
    else
        echo "❌ Error: boxy command not found"
        echo ""
        echo "Please build or install boxy first:"
        echo "  cd /path/to/boxy"
        echo "  task build"
        echo "  export PATH=\$PATH:\$(pwd)"
        exit 1
    fi
fi
echo ""

# Get the directory of this script
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
CONFIG_FILE="$SCRIPT_DIR/boxy.yaml"

echo "📝 Configuration: $CONFIG_FILE"
echo ""

# Clean up any existing database
DB_PATH="$HOME/.config/boxy/quickstart.db"
if [ -f "$DB_PATH" ]; then
    echo "🧹 Cleaning up old database..."
    rm -f "$DB_PATH"
fi

# Clean up any existing workspaces
WORKSPACE_DIR="/tmp/boxy-scratch"
if [ -d "$WORKSPACE_DIR" ]; then
    echo "🧹 Cleaning up old workspaces..."
    rm -rf "$WORKSPACE_DIR"
fi

echo ""
echo "▶️  Starting Boxy..."
echo ""
echo "Press Ctrl+C when done testing."
echo ""
echo "In another terminal, try:"
echo "  boxy sandbox create --pool scratch-pool:1 --duration 30m --name my-test"
echo ""

# Start boxy
# Note: BOXY_CMD is either ./boxy or boxy in PATH
$BOXY_CMD serve --config "$CONFIG_FILE"
