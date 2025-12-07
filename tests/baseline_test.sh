#!/bin/bash
# Stage 0 Baseline Test: Record current behavior before any changes
# This script documents the existing behavior to ensure we don't break anything

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Test session name
TEST_SESSION="baseline-test-$$"
BINARY="./cmd/opencode-tmux/opencode-tmux"

echo "========================================"
echo "TmuxCoder Stage 0 Baseline Test"
echo "========================================"
echo ""
echo "This test records the current behavior before implementing any changes."
echo "Session name: $TEST_SESSION"
echo ""

# Function to cleanup
cleanup() {
    echo ""
    echo -e "${YELLOW}Cleaning up...${NC}"

    # Kill tmux session if it exists
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true

    # Kill any remaining processes
    pkill -f "opencode-tmux.*$TEST_SESSION" || true

    echo -e "${GREEN}Cleanup complete${NC}"
}

# Register cleanup function
trap cleanup EXIT INT TERM

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    echo -e "${RED}Error: Binary not found at $BINARY${NC}"
    echo "Please build the project first: cd cmd/opencode-tmux && go build"
    exit 1
fi

echo "Step 1: Check current git status"
echo "-----------------------------------"
git status --short
git log --oneline -1
echo ""

echo "Step 2: Test basic session creation"
echo "-----------------------------------"
echo "Starting orchestrator with session: $TEST_SESSION"

# Set dummy environment variables for testing
export OPENCODE_SERVER="http://localhost:9999"

# Start in background with --server-only
# Session name is passed as positional argument
$BINARY --server-only "$TEST_SESSION" &
ORCH_PID=$!

echo "Orchestrator PID: $ORCH_PID"
sleep 3

# Check if process is running
if ps -p $ORCH_PID > /dev/null; then
    echo -e "${GREEN}✓ Orchestrator process is running${NC}"
else
    echo -e "${RED}✗ Orchestrator process died${NC}"
    exit 1
fi

echo ""
echo "Step 3: Verify tmux session exists"
echo "-----------------------------------"
if tmux has-session -t "$TEST_SESSION" 2>/dev/null; then
    echo -e "${GREEN}✓ Tmux session exists${NC}"

    # List session details
    echo ""
    echo "Session details:"
    tmux list-sessions | grep "$TEST_SESSION" || true
    echo ""
    echo "Panes in session:"
    tmux list-panes -t "$TEST_SESSION" -F "#{pane_id} #{pane_pid} #{pane_dead}" || true
else
    echo -e "${RED}✗ Tmux session does not exist${NC}"
    exit 1
fi

echo ""
echo "Step 4: Check IPC socket"
echo "-----------------------------------"
SOCKET_PATH="${HOME}/.opencode/${TEST_SESSION}.sock"
if [ -S "$SOCKET_PATH" ]; then
    echo -e "${GREEN}✓ IPC socket exists: $SOCKET_PATH${NC}"
    ls -l "$SOCKET_PATH"
else
    echo -e "${YELLOW}⚠ IPC socket not found (may use different path)${NC}"
    echo "Searching for socket files..."
    find ~/.opencode -name "*.sock" 2>/dev/null || true
fi

echo ""
echo "Step 5: Test signal handling (Ctrl+C simulation)"
echo "-----------------------------------"
echo "Sending SIGINT to orchestrator (PID $ORCH_PID)..."
kill -INT $ORCH_PID

# Wait a bit and check if process terminated
sleep 2

if ps -p $ORCH_PID > /dev/null 2>&1; then
    echo -e "${YELLOW}⚠ Process still running after SIGINT${NC}"
    # Force kill
    kill -9 $ORCH_PID 2>/dev/null || true
else
    echo -e "${GREEN}✓ Process terminated after SIGINT${NC}"
fi

echo ""
echo "Step 6: Check session cleanup after shutdown"
echo "-----------------------------------"
sleep 1

if tmux has-session -t "$TEST_SESSION" 2>/dev/null; then
    echo -e "${YELLOW}⚠ Tmux session still exists after orchestrator shutdown${NC}"
    echo "Current behavior: Session survives orchestrator shutdown"
else
    echo -e "${GREEN}✓ Tmux session cleaned up${NC}"
    echo "Current behavior: Session is destroyed on orchestrator shutdown"
fi

# Check socket cleanup
if [ -S "$SOCKET_PATH" ]; then
    echo -e "${YELLOW}⚠ Socket file still exists${NC}"
else
    echo -e "${GREEN}✓ Socket file cleaned up${NC}"
fi

echo ""
echo "Step 7: Record system information"
echo "-----------------------------------"
echo "OS: $(uname -s)"
echo "Architecture: $(uname -m)"
echo "Tmux version: $(tmux -V)"
echo "Go version: $(go version)"
echo ""

echo "========================================"
echo "Baseline Test Complete"
echo "========================================"
echo ""
echo -e "${GREEN}✓ Baseline behavior recorded${NC}"
echo ""
echo "Summary:"
echo "- Binary location: $BINARY"
echo "- Test session: $TEST_SESSION"
echo "- Orchestrator responded to SIGINT"
echo "- Socket path pattern: ~/.opencode/<session-name>.sock"
echo ""
echo "Next steps:"
echo "1. Review the behavior documented above"
echo "2. Save this output for comparison after implementing changes"
echo "3. Proceed to Stage 1: Infrastructure implementation"
