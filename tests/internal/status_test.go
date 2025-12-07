package internal

import (
    "fmt"
    "net"
    "os"
    "path/filepath"
    "testing"
    "time"
    socketpkg "github.com/opencode/tmux_coder/internal/socket"
)

func TestCheckSocketStatus_NonExistent(t *testing.T) {
	// Test with a path that doesn't exist
	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

    status, err := socketpkg.CheckSocketStatus(socketPath)
	if err != nil {
		t.Errorf("Expected no error for non-existent socket, got: %v", err)
	}
    if status != socketpkg.SocketNonExistent {
		t.Errorf("Expected SocketNonExistent, got: %v", status)
	}
}

func TestCheckSocketStatus_Stale(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "stale.sock")

	// Create a socket file manually (simulating a stale socket)
	// On some systems, closing the listener removes the socket file,
	// so we create it directly as a regular file to test stale detection
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}

	// Get the file info before closing
	info, err := os.Stat(socketPath)
	if err != nil {
		t.Fatalf("Failed to stat socket: %v", err)
	}

	// Close the listener (may or may not remove the file depending on OS)
	listener.Close()

	// If the file was removed, skip this test as we can't create a true stale socket
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("System automatically removes socket file on close, cannot test stale socket")
	}

	// Now the socket file exists but no process is listening
    status, err := socketpkg.CheckSocketStatus(socketPath)
	if err != nil {
		t.Errorf("Expected no error for stale socket, got: %v", err)
	}
    if status != socketpkg.SocketStale {
		t.Errorf("Expected SocketStale, got: %v (info: %v)", status, info.Mode())
	}
}

func TestCheckSocketStatus_Active(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "active.sock")

	// Create a socket with an active listener
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	defer listener.Close()

	// Start accepting connections in background
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	// Give the listener time to start
	time.Sleep(100 * time.Millisecond)

	// Now the socket should be active
    status, err := socketpkg.CheckSocketStatus(socketPath)
	if err != nil {
		t.Errorf("Expected no error for active socket, got: %v", err)
	}
    if status != socketpkg.SocketActive {
		t.Errorf("Expected SocketActive, got: %v", status)
	}
}

func TestCheckSocketStatus_NotASocket(t *testing.T) {
	// Create a regular file (not a socket)
	tmpDir := t.TempDir()
	regularFile := filepath.Join(tmpDir, "regular.txt")

	if err := os.WriteFile(regularFile, []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create regular file: %v", err)
	}

    status, err := socketpkg.CheckSocketStatus(regularFile)
	if err == nil {
		t.Error("Expected error for regular file, got nil")
	}
    if status != socketpkg.SocketNonExistent {
		t.Errorf("Expected SocketNonExistent for regular file, got: %v", status)
	}
}

func TestCleanupStaleSocket_NonExistent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

	// Should succeed without error
    err := socketpkg.CleanupStaleSocket(socketPath)
	if err != nil {
		t.Errorf("Expected no error for non-existent socket, got: %v", err)
	}
}

func TestCleanupStaleSocket_Stale(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "stale.sock")

	// Create a stale socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	listener.Close()

	// If the file was removed by the system, skip this test
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Skip("System automatically removes socket file on close, cannot test stale socket cleanup")
	}

	// Cleanup should succeed
    err = socketpkg.CleanupStaleSocket(socketPath)
	if err != nil {
		t.Errorf("Expected no error cleaning stale socket, got: %v", err)
	}

	// Verify it was deleted
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Socket file should have been deleted")
	}
}

func TestCleanupStaleSocket_Active(t *testing.T) {
	// Use a simpler path that doesn't have nested directories
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-active-%d.sock", time.Now().UnixNano()))

	// Ensure cleanup
	defer os.Remove(socketPath)

	// Create an active socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	defer listener.Close()

	// Start accepting connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	time.Sleep(100 * time.Millisecond)

    // Cleanup should fail
    err = socketpkg.CleanupStaleSocket(socketPath)
	if err == nil {
		t.Error("Expected error cleaning active socket, got nil")
	}

	// Verify it still exists
	if _, err := os.Stat(socketPath); err != nil {
		t.Error("Socket file should still exist")
	}
}

func TestIsSocketAvailable_NonExistent(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "nonexistent.sock")

    available, err := socketpkg.IsSocketAvailable(socketPath)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !available {
		t.Error("Non-existent socket should be available")
	}
}

func TestIsSocketAvailable_Stale(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "stale.sock")

	// Create a stale socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	listener.Close()

	// Should be available after cleanup
    available, err := socketpkg.IsSocketAvailable(socketPath)
	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !available {
		t.Error("Stale socket should be available after cleanup")
	}

	// Verify it was cleaned up
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Error("Stale socket should have been cleaned up")
	}
}

func TestIsSocketAvailable_Active(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "active.sock")

	// Create an active socket
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("Failed to create socket: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	time.Sleep(100 * time.Millisecond)

	// Should not be available
    available, err := socketpkg.IsSocketAvailable(socketPath)
	if err != nil {
		t.Logf("Error (expected): %v", err)
	}
	if available {
		t.Error("Active socket should not be available")
	}
}

func TestSocketStatus_String(t *testing.T) {
    tests := []struct {
        status   socketpkg.SocketStatus
        expected string
    }{
        {socketpkg.SocketNonExistent, "non-existent"},
        {socketpkg.SocketStale, "stale"},
        {socketpkg.SocketActive, "active"},
        {socketpkg.SocketPermissionDenied, "permission-denied"},
    }

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.status.String(); got != tt.expected {
				t.Errorf("SocketStatus.String() = %v, want %v", got, tt.expected)
			}
		})
	}
}
