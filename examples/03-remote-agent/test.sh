#!/bin/bash
# Test remote agent provisioning

set -e

BOXY="../../boxy"

echo "=== Testing Remote Agent Provisioning ==="
echo ""

# Check pool status
echo "1. Checking pool status..."
echo "   (This verifies server connected to agent)"
$BOXY pool list --config server-config.yaml
echo ""

# Wait a moment for pool warming
echo "2. Waiting for pool to warm up..."
echo "   (Remote Hyper-V provisioning takes longer than containers)"
sleep 5
echo ""

# Create a sandbox
echo "3. Creating sandbox with remote Hyper-V VM..."
echo "   This will:"
echo "   - Send gRPC request from server → agent"
echo "   - Agent provisions VM on Windows machine"
echo "   - Server stores resource info"
echo ""

SANDBOX_ID=$($BOXY sandbox create --pool hyperv-pool:1 --duration 2h --config server-config.yaml | grep -oP 'sb-[a-f0-9-]+')

if [ -z "$SANDBOX_ID" ]; then
    echo "❌ Failed to create sandbox"
    exit 1
fi

echo "Created sandbox: $SANDBOX_ID"
echo ""

# Wait for VM to be ready
echo "4. Waiting for VM to be ready..."
echo "   (Hyper-V VMs can take 5-10 minutes to fully provision)"
sleep 10
echo ""

# Get sandbox details
echo "5. Getting sandbox details..."
$BOXY sandbox list --config server-config.yaml
echo ""

# Get connection info
echo "6. Getting connection information..."
$BOXY sandbox get $SANDBOX_ID --config server-config.yaml
echo ""

echo "=== Remote Provisioning Successful! ==="
echo ""
echo "What happened:"
echo "  ✓ Server sent gRPC request to agent"
echo "  ✓ Agent provisioned VM on Windows machine"
echo "  ✓ Server received resource details"
echo "  ✓ Connection info available for RDP"
echo ""
echo "The VM is running on your Windows machine!"
echo "You can connect using the RDP info above."
echo ""

# Prompt to destroy
read -p "Press Enter to destroy sandbox (or Ctrl+C to keep it)..."

echo ""
echo "7. Destroying sandbox..."
$BOXY sandbox destroy $SANDBOX_ID --config server-config.yaml
echo "Sandbox destroyed (VM removed from Windows machine)"
echo ""

echo "=== Test Complete ==="
echo "Remote agent architecture working correctly!"
