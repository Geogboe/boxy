#!/bin/bash
set -e

echo "🚀 Quickstart: Scratch Provider"
echo "================================"
echo ""

# Check if boxy is available
if ! command -v boxy &> /dev/null; then
    echo "❌ Error: boxy command not found"
    echo ""
    echo "Please build or install boxy first:"
    echo "  cd /path/to/boxy"
    echo "  task build"
    echo "  export PATH=\$PATH:\$(pwd)"
    exit 1
fi

echo "✓ Found boxy: $(which boxy)"
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
boxy serve --config "$CONFIG_FILE"
