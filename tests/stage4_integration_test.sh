#!/bin/bash
#
# Stage 4 Integration Test Script
# Tests CLI subcommands functionality
#

set -e

BINARY="./opencode-tmux-new"
TEST_SESSION="stage4-test-$$"
PASSED=0
FAILED=0

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Stage 4 Integration Test Suite"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo

test_case() {
    local name="$1"
    shift
    echo -n "TEST: $name ... "

    if "$@" >/dev/null 2>&1; then
        echo -e "${GREEN}✓ PASS${NC}"
        ((PASSED++))
        return 0
    else
        echo -e "${RED}✗ FAIL${NC}"
        ((FAILED++))
        return 1
    fi
}

test_case_output() {
    local name="$1"
    local expected="$2"
    shift 2
    echo -n "TEST: $name ... "

    output=$("$@" 2>&1)
    if echo "$output" | grep -q "$expected"; then
        echo -e "${GREEN}✓ PASS${NC}"
        ((PASSED++))
        return 0
    else
        echo -e "${RED}✗ FAIL${NC}"
        echo "  Expected: $expected"
        echo "  Got: $output"
        ((FAILED++))
        return 1
    fi
}

# Cleanup function
cleanup() {
    echo
    echo "Cleaning up test environment..."

    # Kill any running test sessions
    tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true

    # Kill any opencode-tmux processes for test session
    pkill -f "opencode-tmux.*$TEST_SESSION" 2>/dev/null || true

    # Remove test session directory
    rm -rf ~/.opencode/"$TEST_SESSION" 2>/dev/null || true

    sleep 1
}

# Ensure clean start
cleanup

echo "━━━ Phase 1: Basic Commands ━━━"
echo

# T1: Help command
test_case_output "help command shows usage" "Commands:" \
    $BINARY help

# T2: Version command
test_case_output "version command shows version" "Version:" \
    $BINARY version

# T3: List command with no sessions
test_case_output "list shows no sessions" "No sessions found" \
    $BINARY list

echo
echo "━━━ Phase 2: Backward Compatibility ━━━"
echo

# Note: We can't easily test legacy mode without OPENCODE_SERVER set
echo "SKIP: Legacy mode requires OPENCODE_SERVER environment variable"

echo
echo "━━━ Phase 2.5: Session Lifecycle ━━━"
echo

# Set required environment variable for daemon mode
export OPENCODE_SERVER="${OPENCODE_SERVER:-http://localhost:9999}"

# T8: Start a session with --daemon (creates tmux session)
echo -n "Starting test session... "
$BINARY start "$TEST_SESSION" --daemon >/dev/null 2>&1 &
sleep 3
echo "done"

# T9: List should show the running session
test_case_output "list shows running session" "$TEST_SESSION" \
    $BINARY list

# T10: Status should show running
test_case_output "status shows daemon running" "Running" \
    $BINARY status "$TEST_SESSION"

# T11: Stop the daemon (keeps tmux session running = orphaned)
echo -n "Stopping daemon... "
$BINARY stop "$TEST_SESSION" >/dev/null 2>&1 || true
sleep 2
echo "done"

# T12: List should show orphaned session (tmux running, daemon stopped)
test_case_output "list shows orphaned session" "$TEST_SESSION" \
    $BINARY list

# T13: Status should show orphaned
test_case_output "status shows orphaned session" "Orphaned" \
    $BINARY status "$TEST_SESSION"

# Clean up for next phase
echo -n "Cleaning up test session... "
tmux kill-session -t "$TEST_SESSION" 2>/dev/null || true
sleep 1
echo "done"

echo
echo "━━━ Phase 3: Subcommand Help ━━━"
echo

# T14: Each subcommand has help
test_case_output "attach --help works" "Usage:" \
    $BINARY attach --help

test_case_output "detach --help works" "Usage:" \
    $BINARY detach --help

test_case_output "stop --help works" "Usage:" \
    $BINARY stop --help

test_case_output "status --help works" "Usage:" \
    $BINARY status --help

test_case_output "list --help works" "Usage:" \
    $BINARY list --help

echo
echo "━━━ Phase 4: Error Handling ━━━"
echo

# T15: Attach to non-existent session
test_case_output "attach to non-existent session fails" "does not exist" \
    bash -c "$BINARY attach nonexistent-session-xyz 2>&1 || true"

# T16: Status of non-existent session
test_case_output "status of non-existent shows stopped" "Stopped" \
    $BINARY status nonexistent-session-xyz

# T17: Stop non-running daemon
test_case_output "stop non-running daemon fails" "not running" \
    bash -c "$BINARY stop nonexistent-session-xyz 2>&1 || true"

echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Test Summary"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo -e "${GREEN}PASSED: $PASSED${NC}"
echo -e "${RED}FAILED: $FAILED${NC}"
echo "TOTAL:  $((PASSED + FAILED))"
echo

# Final cleanup
cleanup

if [ $FAILED -eq 0 ]; then
    echo -e "${GREEN}All tests passed!${NC}"
    exit 0
else
    echo -e "${RED}Some tests failed!${NC}"
    exit 1
fi
