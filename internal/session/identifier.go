package session

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// SessionID represents a tmux session identifier
type SessionID struct {
	Name      string    // Session name (e.g., "project-a")
	TmuxID    string    // Tmux internal ID (from $TMUX environment variable)
	StartTime time.Time // Session start time
}

// GetCurrentSessionID retrieves the current tmux session identifier
func GetCurrentSessionID() (*SessionID, error) {
	// Check if running inside tmux
	tmuxVar := os.Getenv("TMUX")
	if tmuxVar == "" {
		return nil, fmt.Errorf("not in tmux session")
	}

	// Get session name
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get session name: %w", err)
	}

	sessionName := strings.TrimSpace(string(output))
	if sessionName == "" {
		return nil, fmt.Errorf("empty session name")
	}

	// Get session creation time
	cmd = exec.Command("tmux", "display-message", "-p", "#{session_created}")
	output, err = cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get session created time: %w", err)
	}

	var timestamp int64
	fmt.Sscanf(string(output), "%d", &timestamp)

	return &SessionID{
		Name:      sessionName,
		TmuxID:    tmuxVar,
		StartTime: time.Unix(timestamp, 0),
	}, nil
}

// Validate verifies that the session still exists
func (s *SessionID) Validate() error {
	cmd := exec.Command("tmux", "has-session", "-t", s.Name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("session %s does not exist", s.Name)
	}
	return nil
}

// String returns the string representation of the session
func (s *SessionID) String() string {
	return s.Name
}
