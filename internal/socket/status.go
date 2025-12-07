package socket

import (
	"fmt"
	"net"
	"os"
	"time"
)

// SocketStatus represents the status of a socket file
type SocketStatus int

const (
	// SocketNonExistent indicates the socket file doesn't exist
	SocketNonExistent SocketStatus = iota
	// SocketStale indicates the socket file exists but no process is listening (can be safely deleted)
	SocketStale
	// SocketActive indicates the socket file exists and a process is listening (cannot be deleted)
	SocketActive
	// SocketPermissionDenied indicates the socket file exists but we don't have permission to access it
	SocketPermissionDenied
)

// String returns the string representation of SocketStatus
func (s SocketStatus) String() string {
	switch s {
	case SocketNonExistent:
		return "non-existent"
	case SocketStale:
		return "stale"
	case SocketActive:
		return "active"
	case SocketPermissionDenied:
		return "permission-denied"
	default:
		return "unknown"
	}
}

// CheckSocketStatus checks the status of a socket file
func CheckSocketStatus(socketPath string) (SocketStatus, error) {
	// 1. Check if file exists
	info, err := os.Stat(socketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return SocketNonExistent, nil
		}
		if os.IsPermission(err) {
			return SocketPermissionDenied, err
		}
		return SocketNonExistent, err
	}

	// 2. Check if it's a socket file
	if info.Mode()&os.ModeSocket == 0 {
		// File exists but is not a socket - treat as stale (leftover from crash/improper cleanup)
		return SocketStale, fmt.Errorf("%s exists but is not a socket file (treating as stale)", socketPath)
	}

	// 3. Try connecting to check if a process is listening
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		// Connection failed, no process listening (stale socket)
		return SocketStale, nil
	}
	conn.Close()

	// 4. A process is listening
	return SocketActive, nil
}

// CleanupStaleSocket removes a stale socket file after verifying it's safe to do so
func CleanupStaleSocket(socketPath string) error {
	status, err := CheckSocketStatus(socketPath)
	if err != nil && status != SocketStale {
		return err
	}

	switch status {
	case SocketNonExistent:
		// Doesn't exist, no cleanup needed
		return nil

	case SocketStale:
		// Stale socket, safe to delete
		if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove stale socket: %w", err)
		}
		return nil

	case SocketActive:
		// Process is using it, cannot delete
		return fmt.Errorf("socket %s is in use by another process", socketPath)

	case SocketPermissionDenied:
		// Permission issue
		return fmt.Errorf("permission denied to access socket %s", socketPath)

	default:
		return fmt.Errorf("unknown socket status")
	}
}

// IsSocketAvailable checks if a socket path is available for use
// Returns true if the socket doesn't exist or is stale (and was successfully cleaned up)
func IsSocketAvailable(socketPath string) (bool, error) {
	status, err := CheckSocketStatus(socketPath)
	if err != nil && status != SocketStale {
		return false, err
	}

	switch status {
	case SocketNonExistent:
		return true, nil

	case SocketStale:
		// Try to cleanup
		if err := CleanupStaleSocket(socketPath); err != nil {
			return false, fmt.Errorf("failed to cleanup stale socket: %w", err)
		}
		return true, nil

	case SocketActive:
		return false, nil

	case SocketPermissionDenied:
		return false, fmt.Errorf("permission denied")

	default:
		return false, fmt.Errorf("unknown socket status")
	}
}
