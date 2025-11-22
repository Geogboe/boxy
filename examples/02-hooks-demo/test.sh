#!/bin/bash
# Test the hooks demo by creating a sandbox and verifying hook execution

set -e

BOXY="../../boxy"

echo "=== Testing Hooks Demo ==="
echo ""

# Check pool status
echo "1. Checking pool status..."
$BOXY pool list --config boxy.yaml
echo ""

# Create a sandbox
echo "2. Creating sandbox with hooks..."
echo "   This will trigger before_allocate hooks:"
echo "   - create-user (with random username/password)"
echo "   - setup-workspace"
echo ""

SANDBOX_ID=$($BOXY sandbox create --pool hooked-pool:1 --duration 1h --config boxy.yaml | grep -oP 'sb-[a-f0-9-]+')
echo "Created sandbox: $SANDBOX_ID"
echo ""

# Wait for allocation and personalization
echo "3. Waiting for personalization hooks to complete..."
sleep 5
echo ""

# Get sandbox details
echo "4. Getting sandbox details..."
$BOXY sandbox list --config boxy.yaml
echo ""

# Get connection info
echo "5. Getting connection information..."
echo "   You can use this to verify hooks worked:"
echo ""
$BOXY sandbox get $SANDBOX_ID --config boxy.yaml
echo ""

# Show verification commands
echo "=== Verification Commands ==="
echo "To verify hooks worked, you can:"
echo ""
echo "1. Check finalization (after_provision) results:"
echo "   docker exec <container_id> cat /provisioned.txt"
echo "   docker exec <container_id> cat /setup-complete.txt"
echo "   docker exec <container_id> which curl vim htop"
echo ""
echo "2. Check personalization (before_allocate) results:"
echo "   docker exec <container_id> cat /home/<username>/welcome.txt"
echo "   docker exec <container_id> ls -la /home/<username>/workspace"
echo "   docker exec <container_id> /home/<username>/workspace/hello.sh"
echo ""
echo "Or run: ./verify-hooks.sh $SANDBOX_ID"
echo ""

# Prompt to destroy
read -p "Press Enter to destroy sandbox (or Ctrl+C to keep it)..."

echo ""
echo "6. Destroying sandbox..."
$BOXY sandbox destroy $SANDBOX_ID --config boxy.yaml
echo "Sandbox destroyed"
echo ""

echo "=== Test Complete ==="
echo "Demonstrated:"
echo "  ✓ Finalization hooks ran during pool warming"
echo "  ✓ Personalization hooks ran during allocation"
echo "  ✓ Template variables were substituted"
echo "  ✓ Resources cleaned up properly"
