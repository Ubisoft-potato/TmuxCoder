package client

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ClientTracker tracks tmux client connections for a session
type ClientTracker struct {
	sessionName   string
	tmuxCommand   string
	checkInterval time.Duration

	mu          sync.RWMutex
	clientCount int
	lastCheck   time.Time
	lastError   error
}

// ClientInfo represents information about a connected tmux client
type ClientInfo struct {
	TTY         string
	PID         string
	Created     string
	SessionName string
}

// NewClientTracker creates a new client tracker
func NewClientTracker(sessionName, tmuxCommand string, checkInterval time.Duration) *ClientTracker {
	if tmuxCommand == "" {
		tmuxCommand = "tmux"
	}
	if checkInterval == 0 {
		checkInterval = 5 * time.Second
	}

	return &ClientTracker{
		sessionName:   sessionName,
		tmuxCommand:   tmuxCommand,
		checkInterval: checkInterval,
	}
}

// GetConnectedClients returns the number of currently connected clients
func (ct *ClientTracker) GetConnectedClients() (int, error) {
	cmd := exec.Command(ct.tmuxCommand, "list-clients", "-t", ct.sessionName)
	output, err := cmd.Output()
	if err != nil {
		// Session doesn't exist or has no clients
		if exitErr, ok := err.(*exec.ExitError); ok {
			// Exit code 1 usually means session not found or no clients
			if exitErr.ExitCode() == 1 {
				return 0, nil
			}
		}
		return 0, fmt.Errorf("failed to list clients: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}

	return count, nil
}

// GetConnectedClientsInfo returns detailed information about connected clients
func (ct *ClientTracker) GetConnectedClientsInfo() ([]ClientInfo, error) {
	// Format: #{client_tty} #{client_pid} #{client_created} #{session_name}
	format := "#{client_tty} #{client_pid} #{client_created} #{session_name}"
	cmd := exec.Command(ct.tmuxCommand, "list-clients", "-t", ct.sessionName, "-F", format)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			if exitErr.ExitCode() == 1 {
				return []ClientInfo{}, nil
			}
		}
		return nil, fmt.Errorf("failed to list clients: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	clients := make([]ClientInfo, 0, len(lines))

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 3 {
			clients = append(clients, ClientInfo{
				TTY:         parts[0],
				PID:         parts[1],
				Created:     parts[2],
				SessionName: ct.sessionName,
			})
		}
	}

	return clients, nil
}

// MonitorClients periodically checks client connection status
// Calls the callback function with the current client count
func (ct *ClientTracker) MonitorClients(ctx context.Context, callback func(count int)) {
	ticker := time.NewTicker(ct.checkInterval)
	defer ticker.Stop()

	// Do an initial check
	count, err := ct.GetConnectedClients()
	if err == nil {
		ct.mu.Lock()
		ct.clientCount = count
		ct.lastCheck = time.Now()
		ct.lastError = nil
		ct.mu.Unlock()

		if callback != nil {
			callback(count)
		}
	} else {
		ct.mu.Lock()
		ct.lastError = err
		ct.mu.Unlock()
		log.Printf("[ClientTracker] Initial check failed: %v", err)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			count, err := ct.GetConnectedClients()
			if err == nil {
				ct.mu.Lock()
				oldCount := ct.clientCount
				ct.clientCount = count
				ct.lastCheck = time.Now()
				ct.lastError = nil
				ct.mu.Unlock()

				// Only callback if count changed or this is first successful check
				if callback != nil && (count != oldCount || oldCount == 0) {
					callback(count)
				}
			} else {
				ct.mu.Lock()
				ct.lastError = err
				ct.mu.Unlock()
			}
		}
	}
}

// GetLastKnownCount returns the last checked client count (no wait)
func (ct *ClientTracker) GetLastKnownCount() (int, time.Time) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.clientCount, ct.lastCheck
}

// GetLastError returns the last error encountered during monitoring
func (ct *ClientTracker) GetLastError() error {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return ct.lastError
}

// HasClients returns true if there are any connected clients (based on last check)
func (ct *ClientTracker) HasClients() bool {
    ct.mu.RLock()
    defer ct.mu.RUnlock()
    return ct.clientCount > 0
}

func (ct *ClientTracker) SessionName() string { return ct.sessionName }

func (ct *ClientTracker) TmuxCommand() string { return ct.tmuxCommand }

func (ct *ClientTracker) CheckInterval() time.Duration { return ct.checkInterval }

func (ct *ClientTracker) SetTestingState(count int, lastCheck time.Time, err error) {
    ct.mu.Lock()
    ct.clientCount = count
    ct.lastCheck = lastCheck
    ct.lastError = err
    ct.mu.Unlock()
}

// String returns a human-readable representation of the tracker state
func (ct *ClientTracker) String() string {
    ct.mu.RLock()
    defer ct.mu.RUnlock()

    if ct.lastError != nil {
        return fmt.Sprintf("ClientTracker[session=%s, last_error=%v]", ct.sessionName, ct.lastError)
    }

    return fmt.Sprintf("ClientTracker[session=%s, clients=%d, last_check=%v]",
        ct.sessionName, ct.clientCount, ct.lastCheck.Format(time.RFC3339))
}
