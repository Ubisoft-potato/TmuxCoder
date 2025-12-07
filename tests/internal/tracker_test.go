package internal

import (
    "context"
    "os"
    "os/exec"
    "strings"
    "testing"
    "time"
    clientpkg "github.com/opencode/tmux_coder/internal/client"
)

// Helper to check if tmux is available
func isTmuxAvailable() bool {
	cmd := exec.Command("tmux", "-V")
	return cmd.Run() == nil
}

// Helper to create a test tmux session
func createTestSession(t *testing.T, sessionName string) {
	cmd := exec.Command("tmux", "new-session", "-d", "-s", sessionName)
	if err := cmd.Run(); err != nil {
		t.Fatalf("Failed to create test session: %v", err)
	}
}

// Helper to kill a test tmux session
func killTestSession(t *testing.T, sessionName string) {
	exec.Command("tmux", "kill-session", "-t", sessionName).Run()
}

func TestNewClientTracker(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "tmux", 5*time.Second)

    if tracker.SessionName() != "test-session" {
        t.Errorf("Expected sessionName 'test-session', got '%s'", tracker.SessionName())
    }
    if tracker.TmuxCommand() != "tmux" {
        t.Errorf("Expected tmuxCommand 'tmux', got '%s'", tracker.TmuxCommand())
    }
    if tracker.CheckInterval() != 5*time.Second {
        t.Errorf("Expected checkInterval 5s, got %v", tracker.CheckInterval())
    }
}

func TestNewClientTracker_Defaults(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "", 0)

    if tracker.TmuxCommand() != "tmux" {
        t.Errorf("Expected default tmuxCommand 'tmux', got '%s'", tracker.TmuxCommand())
    }
    if tracker.CheckInterval() != 5*time.Second {
        t.Errorf("Expected default checkInterval 5s, got %v", tracker.CheckInterval())
    }
}

func TestGetConnectedClients_NoSession(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

    tracker := clientpkg.NewClientTracker("nonexistent-session-"+time.Now().Format("20060102150405"), "tmux", 5*time.Second)

	count, err := tracker.GetConnectedClients()
	if err != nil {
		t.Errorf("Expected no error for nonexistent session, got: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 clients for nonexistent session, got %d", count)
	}
}

func TestGetConnectedClients_EmptySession(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := "test-empty-" + time.Now().Format("20060102150405")
	createTestSession(t, sessionName)
	defer killTestSession(t, sessionName)

    tracker := clientpkg.NewClientTracker(sessionName, "tmux", 5*time.Second)

	count, err := tracker.GetConnectedClients()
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	// Detached session should have 0 clients
	if count != 0 {
		t.Logf("Note: Detached session has %d client(s), expected 0", count)
	}
}

func TestGetConnectedClientsInfo_NoSession(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

    tracker := clientpkg.NewClientTracker("nonexistent-session-"+time.Now().Format("20060102150405"), "tmux", 5*time.Second)

	clients, err := tracker.GetConnectedClientsInfo()
	if err != nil {
		t.Errorf("Expected no error for nonexistent session, got: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("Expected 0 clients for nonexistent session, got %d", len(clients))
	}
}

func TestGetLastKnownCount(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "tmux", 5*time.Second)

	// Initially should be 0
	count, lastCheck := tracker.GetLastKnownCount()
	if count != 0 {
		t.Errorf("Expected initial count 0, got %d", count)
	}
	if !lastCheck.IsZero() {
		t.Errorf("Expected zero time for lastCheck, got %v", lastCheck)
	}

    tracker.SetTestingState(5, time.Now(), nil)

	count, lastCheck = tracker.GetLastKnownCount()
	if count != 5 {
		t.Errorf("Expected count 5, got %d", count)
	}
	if lastCheck.IsZero() {
		t.Error("Expected non-zero lastCheck")
	}
}

func TestHasClients(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "tmux", 5*time.Second)

	// Initially should be false
	if tracker.HasClients() {
		t.Error("Expected HasClients() to be false initially")
	}

    tracker.SetTestingState(3, time.Now(), nil)

	if !tracker.HasClients() {
		t.Error("Expected HasClients() to be true with 3 clients")
	}

    tracker.SetTestingState(0, time.Now(), nil)

	if tracker.HasClients() {
		t.Error("Expected HasClients() to be false with 0 clients")
	}
}

func TestGetLastError(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "tmux", 5*time.Second)

	// Initially should be nil
	if tracker.GetLastError() != nil {
		t.Errorf("Expected nil error initially, got %v", tracker.GetLastError())
	}

    testErr := os.ErrNotExist
    tracker.SetTestingState(0, time.Now(), testErr)

	if tracker.GetLastError() != testErr {
		t.Errorf("Expected error %v, got %v", testErr, tracker.GetLastError())
	}
}

func TestString(t *testing.T) {
    tracker := clientpkg.NewClientTracker("test-session", "tmux", 5*time.Second)

	// Test with no error
	str := tracker.String()
	if !strings.Contains(str, "test-session") {
		t.Errorf("Expected string to contain session name, got: %s", str)
	}
	if !strings.Contains(str, "clients=0") {
		t.Errorf("Expected string to contain client count, got: %s", str)
	}

    tracker.SetTestingState(0, time.Now(), os.ErrNotExist)

	str = tracker.String()
	if !strings.Contains(str, "last_error") {
		t.Errorf("Expected string to contain error, got: %s", str)
	}
}

func TestMonitorClients_Context(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := "test-monitor-" + time.Now().Format("20060102150405")
	createTestSession(t, sessionName)
	defer killTestSession(t, sessionName)

    tracker := clientpkg.NewClientTracker(sessionName, "tmux", 100*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())

	callbackCount := 0
	go tracker.MonitorClients(ctx, func(count int) {
		callbackCount++
	})

	// Let it run a bit
	time.Sleep(300 * time.Millisecond)

	// Cancel context
	cancel()

	// Give it time to stop
	time.Sleep(100 * time.Millisecond)

	// Should have been called at least once (initial check)
	if callbackCount == 0 {
		t.Error("Expected callback to be called at least once")
	}

	t.Logf("Callback was called %d times", callbackCount)
}

func TestMonitorClients_NoCallback(t *testing.T) {
	if !isTmuxAvailable() {
		t.Skip("tmux not available")
	}

	sessionName := "test-monitor-nocb-" + time.Now().Format("20060102150405")
	createTestSession(t, sessionName)
	defer killTestSession(t, sessionName)

    tracker := clientpkg.NewClientTracker(sessionName, "tmux", 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	// Should not panic with nil callback
	tracker.MonitorClients(ctx, nil)

	// Wait for context to complete
	<-ctx.Done()

	// Check that monitoring updated the internal state
	count, lastCheck := tracker.GetLastKnownCount()
	if lastCheck.IsZero() {
		t.Error("Expected lastCheck to be set after monitoring")
	}

	t.Logf("Final state: %d clients, last check: %v", count, lastCheck)
}
