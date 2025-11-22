#!/bin/bash
# Verify that hooks executed successfully inside a sandbox

set -e

BOXY="../../boxy"
SANDBOX_ID=$1

if [ -z "$SANDBOX_ID" ]; then
    echo "Usage: $0 <sandbox_id>"
    echo ""
    echo "Example: $0 sb-12345678-1234-1234-1234-123456789abc"
    exit 1
fi

echo "=== Verifying Hooks for Sandbox: $SANDBOX_ID ==="
echo ""

# Get sandbox info to find container ID
echo "1. Getting sandbox information..."
SANDBOX_INFO=$($BOXY sandbox get $SANDBOX_ID --config boxy.yaml)
echo "$SANDBOX_INFO"
echo ""

# Extract container ID from Docker (this is a bit hacky but works for demo)
CONTAINER_ID=$(docker ps --format '{{.ID}} {{.Labels}}' | grep "$SANDBOX_ID" | cut -d' ' -f1 | head -1)

if [ -z "$CONTAINER_ID" ]; then
    echo "❌ Could not find container for sandbox $SANDBOX_ID"
    echo "   Is the sandbox still running?"
    exit 1
fi

echo "Found container: $CONTAINER_ID"
echo ""

# Verify finalization hooks
echo "2. Verifying finalization hooks (after_provision)..."
echo ""

echo "   a) Check provisioning marker:"
docker exec $CONTAINER_ID cat /provisioned.txt 2>/dev/null || echo "   ❌ /provisioned.txt not found"
echo ""

echo "   b) Check directory setup:"
docker exec $CONTAINER_ID cat /setup-complete.txt 2>/dev/null || echo "   ❌ /setup-complete.txt not found"
echo ""

echo "   c) Check installed tools:"
for tool in curl vim htop; do
    if docker exec $CONTAINER_ID which $tool >/dev/null 2>&1; then
        echo "   ✓ $tool installed"
    else
        echo "   ❌ $tool not installed"
    fi
done
echo ""

echo "   d) Check directories created:"
for dir in /app /data /logs; do
    if docker exec $CONTAINER_ID test -d $dir; then
        echo "   ✓ $dir exists"
    else
        echo "   ❌ $dir not found"
    fi
done
echo ""

# Verify personalization hooks
echo "3. Verifying personalization hooks (before_allocate)..."
echo ""

echo "   a) Check for created user (searching for recent users):"
docker exec $CONTAINER_ID cat /etc/passwd | grep -E "/home/[^:]+:[^:]+$" | tail -5
echo ""

echo "   b) Looking for welcome.txt files:"
docker exec $CONTAINER_ID find /home -name "welcome.txt" -type f 2>/dev/null || echo "   No welcome.txt files found"
echo ""

echo "   c) Looking for workspace directories:"
docker exec $CONTAINER_ID find /home -name "workspace" -type d 2>/dev/null || echo "   No workspace directories found"
echo ""

# Try to find the actual username and show details
USERNAME=$(docker exec $CONTAINER_ID find /home -maxdepth 1 -mindepth 1 -type d 2>/dev/null | head -1 | xargs basename 2>/dev/null || echo "")

if [ -n "$USERNAME" ]; then
    echo "   d) Found user: $USERNAME"
    echo ""
    echo "   e) Welcome message contents:"
    docker exec $CONTAINER_ID cat /home/$USERNAME/welcome.txt 2>/dev/null || echo "   ❌ Welcome file not found"
    echo ""

    echo "   f) Workspace contents:"
    docker exec $CONTAINER_ID ls -la /home/$USERNAME/workspace 2>/dev/null || echo "   ❌ Workspace not found"
    echo ""

    echo "   g) Testing hello.sh script:"
    docker exec $CONTAINER_ID /home/$USERNAME/workspace/hello.sh 2>/dev/null || echo "   ❌ Script not executable or not found"
    echo ""
fi

echo "=== Verification Complete ==="
echo ""
echo "Summary:"
echo "  - Finalization hooks create system-wide resources"
echo "  - Personalization hooks create user-specific resources"
echo "  - Template variables (username, password, resource.id) are substituted"
echo "  - All changes persist in the running container"
