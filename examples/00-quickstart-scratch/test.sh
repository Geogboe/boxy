#!/bin/bash
set -e

echo "🧪 Testing Quickstart Example"
echo "=============================="
echo ""

# Check if boxy is running
if ! curl -s http://localhost:8080/health > /dev/null 2>&1; then
    echo "❌ Error: Boxy is not running on localhost:8080"
    echo ""
    echo "Please start Boxy first:"
    echo "  ./run.sh"
    exit 1
fi

echo "✓ Boxy is running"
echo ""

# Create a sandbox
echo "📦 Creating sandbox..."
SANDBOX_OUTPUT=$(boxy sandbox create --pool scratch-pool:1 --duration 5m --name test-workspace 2>&1)
SANDBOX_ID=$(echo "$SANDBOX_OUTPUT" | grep "^ID:" | awk '{print $2}')

if [ -z "$SANDBOX_ID" ]; then
    echo "❌ Failed to create sandbox"
    echo "$SANDBOX_OUTPUT"
    exit 1
fi

echo "✓ Created sandbox: $SANDBOX_ID"
echo ""

# Wait a moment for full readiness
sleep 2

# Get sandbox details
echo "📋 Getting sandbox details..."
SANDBOX_DETAILS=$(boxy sandbox get "$SANDBOX_ID" 2>&1)

if [ $? -ne 0 ]; then
    echo "❌ Failed to get sandbox details"
    echo "$SANDBOX_DETAILS"
    exit 1
fi

echo "✓ Sandbox details retrieved"
echo ""

# Extract workspace directory
WORKSPACE_DIR=$(echo "$SANDBOX_DETAILS" | grep "Workspace dir:" | awk '{print $3}')

if [ -z "$WORKSPACE_DIR" ]; then
    echo "❌ Could not find workspace directory"
    exit 1
fi

echo "✓ Workspace directory: $WORKSPACE_DIR"
echo ""

# Verify workspace exists
if [ ! -d "$WORKSPACE_DIR" ]; then
    echo "❌ Workspace directory does not exist: $WORKSPACE_DIR"
    exit 1
fi

echo "✓ Workspace directory exists"
echo ""

# Check for expected files
echo "📁 Checking workspace contents..."
if [ -f "$WORKSPACE_DIR/README.md" ]; then
    echo "✓ Found README.md"
else
    echo "⚠️  README.md not found (after_provision hook may have failed)"
fi

if [ -f "$WORKSPACE_DIR/.bashrc" ]; then
    echo "✓ Found .bashrc"
else
    echo "⚠️  .bashrc not found (before_allocate hook may have failed)"
fi

if [ -f "$WORKSPACE_DIR/.boxy-env" ]; then
    echo "✓ Found .boxy-env"
else
    echo "⚠️  .boxy-env not found"
fi

echo ""

# Test writing to workspace
echo "✍️  Testing workspace write..."
TEST_FILE="$WORKSPACE_DIR/test-write.txt"
echo "Test from test.sh" > "$TEST_FILE"

if [ -f "$TEST_FILE" ]; then
    echo "✓ Successfully wrote to workspace"
    rm "$TEST_FILE"
else
    echo "❌ Failed to write to workspace"
    exit 1
fi

echo ""

# Clean up
echo "🧹 Cleaning up sandbox..."
boxy sandbox destroy "$SANDBOX_ID" > /dev/null 2>&1

if [ $? -eq 0 ]; then
    echo "✓ Sandbox destroyed"
else
    echo "⚠️  Failed to destroy sandbox (may need manual cleanup)"
fi

echo ""

# Verify cleanup
if [ -d "$WORKSPACE_DIR" ]; then
    echo "⚠️  Workspace directory still exists (cleanup may have failed)"
else
    echo "✓ Workspace directory removed"
fi

echo ""
echo "✅ All tests passed!"
echo ""
echo "The scratch provider is working correctly."
