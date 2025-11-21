package session

import (
	"fmt"
	"os"
	"syscall"
	"time"
)

// SessionLock represents a session lock
type SessionLock struct {
	lockFile *os.File
	pidPath  string
}

var ErrAlreadyRunning = fmt.Errorf("orchestrator already running for this session")

// AcquireLock attempts to acquire a session lock
func AcquireLock(pidPath string) (*SessionLock, error) {
	// Try to create PID file (exclusive mode)
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		if os.IsExist(err) {
			// Check if process is still running
			if isProcessAlive(pidPath) {
				return nil, ErrAlreadyRunning
			}

			// Cleanup stale lock
			os.Remove(pidPath)
			return AcquireLock(pidPath)
		}
		return nil, fmt.Errorf("failed to create lock file: %w", err)
	}

	// Write current process PID and start time to detect PID reuse
	fmt.Fprintf(f, "%d %d\n", os.Getpid(), time.Now().Unix())
	f.Sync()

	return &SessionLock{
		lockFile: f,
		pidPath:  pidPath,
	}, nil
}

// Release releases the lock
func (l *SessionLock) Release() error {
	if l.lockFile != nil {
		l.lockFile.Close()
		os.Remove(l.pidPath)
	}
	return nil
}

// CheckLock checks if a lock file exists and if the process is running
// Returns (PID, isRunning)
func CheckLock(pidPath string) (int, bool) {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, false
	}

	var pid int
	var startTime int64
	fmt.Sscanf(string(data), "%d %d", &pid, &startTime)

	if pid == 0 {
		return 0, false
	}

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return pid, false
	}

	// Send signal 0 to detect liveness
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return pid, false
	}

	// If start time was recorded, check if lock file is stale (older than 24 hours)
	// This helps detect PID reuse scenarios
	if startTime > 0 {
		lockAge := time.Now().Unix() - startTime
		if lockAge > 24*60*60 { // 24 hours
			return pid, false // Treat as stale
		}
	}

	return pid, true
}

// StopProcess stops a process by PID using SIGTERM, then SIGKILL if needed
func StopProcess(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid PID: %d", pid)
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("process not found: %w", err)
	}

	// First try SIGTERM (graceful shutdown)
	if err := process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Wait up to 5 seconds for process to terminate
	for i := 0; i < 50; i++ {
		if err := process.Signal(syscall.Signal(0)); err != nil {
			// Process is dead
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	// If still running, use SIGKILL
	if err := process.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("failed to send SIGKILL: %w", err)
	}

	return nil
}

// isProcessAlive checks if the process in PID file is alive
func isProcessAlive(pidPath string) bool {
	_, running := CheckLock(pidPath)
	return running
}
