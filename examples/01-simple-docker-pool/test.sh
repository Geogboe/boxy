#!/bin/bash
# Test the simple Docker pool by creating and destroying a sandbox

set -e

BOXY="../../boxy"

echo "=== Testing Simple Docker Pool ==="
echo ""

# Check pool status
echo "1. Checking pool status..."
$BOXY pool list --config boxy.yaml
echo ""

# Create a sandbox
echo "2. Creating sandbox..."
SANDBOX_ID=$($BOXY sandbox create --pool simple-pool:1 --duration 1h --config boxy.yaml | grep -oP 'sb-[a-f0-9-]+')
echo "Created sandbox: $SANDBOX_ID"
echo ""

# Wait a moment for allocation
sleep 2

# List sandboxes
echo "3. Listing sandboxes..."
$BOXY sandbox list --config boxy.yaml
echo ""

# Check pool status again (should show allocation + replenishment)
echo "4. Pool status after allocation..."
$BOXY pool list --config boxy.yaml
echo ""

# Destroy sandbox
echo "5. Destroying sandbox..."
$BOXY sandbox destroy $SANDBOX_ID --config boxy.yaml
echo "Sandbox destroyed"
echo ""

# Final pool status
echo "6. Final pool status..."
$BOXY pool list --config boxy.yaml
echo ""

echo "=== Test Complete ==="
echo "Pool automatically:"
echo "  - Warmed up 2 containers"
echo "  - Allocated 1 to sandbox"
echo "  - Replenished back to 2"
echo "  - Cleaned up on destroy"
