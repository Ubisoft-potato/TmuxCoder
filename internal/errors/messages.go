package errors

import "fmt"

// UserFriendlyError provides an error with helpful hints for the user
type UserFriendlyError struct {
	Message string   // The primary error message
	Hints   []string // Actionable suggestions for the user
	Cause   error    // The underlying error (if any)
}

// Error implements the error interface
func (e *UserFriendlyError) Error() string {
	msg := e.Message
	if e.Cause != nil {
		msg += fmt.Sprintf(": %v", e.Cause)
	}
	if len(e.Hints) > 0 {
		msg += "\n\nSuggested actions:"
		for _, hint := range e.Hints {
			msg += fmt.Sprintf("\n  → %s", hint)
		}
	}
	return msg
}

// Unwrap returns the underlying error for errors.Is/As compatibility
func (e *UserFriendlyError) Unwrap() error {
	return e.Cause
}

// SessionNotFound creates an error for when a tmux session doesn't exist
func SessionNotFound(sessionName string) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Session '%s' is not running", sessionName),
		Hints: []string{
			fmt.Sprintf("Start the session: opencode-tmux start %s", sessionName),
			"List all sessions: opencode-tmux list",
			"Check running tmux sessions: tmux ls",
		},
	}
}

// DaemonNotRunning creates an error for when the daemon process isn't running
func DaemonNotRunning(sessionName, socketPath string) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Daemon for session '%s' is not running", sessionName),
		Hints: []string{
			fmt.Sprintf("Check socket status: ls -l %s", socketPath),
			fmt.Sprintf("Start the daemon: opencode-tmux start %s", sessionName),
			fmt.Sprintf("If session exists, reconnect: opencode-tmux start %s --reuse", sessionName),
		},
	}
}

// SessionAlreadyExists creates an error for when trying to create a session that exists
func SessionAlreadyExists(sessionName string) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Session '%s' already exists", sessionName),
		Hints: []string{
			fmt.Sprintf("Reuse existing session: opencode-tmux start %s --reuse", sessionName),
			fmt.Sprintf("Force new session: opencode-tmux start %s --force", sessionName),
			fmt.Sprintf("Attach to existing: opencode-tmux attach %s", sessionName),
		},
	}
}

// SocketConflict creates an error for socket conflicts
func SocketConflict(socketPath string, ownerPID int) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Socket conflict: another daemon is using %s (PID %d)", socketPath, ownerPID),
		Hints: []string{
			"Check running daemons: ps aux | grep opencode-tmux",
			fmt.Sprintf("Stop conflicting daemon: kill %d", ownerPID),
			"Use a different session name to avoid conflicts",
		},
	}
}

// PermissionDenied creates an error for permission issues
func PermissionDenied(operation, reason string) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Permission denied for operation '%s': %s", operation, reason),
		Hints: []string{
			"Check that you own the session",
			"Verify socket permissions",
			"Contact the session owner if needed",
		},
	}
}

// TmuxNotAvailable creates an error when tmux is not installed or not in PATH
func TmuxNotAvailable() *UserFriendlyError {
	return &UserFriendlyError{
		Message: "tmux is not available on this system",
		Hints: []string{
			"Install tmux: brew install tmux (macOS) or apt-get install tmux (Linux)",
			"Ensure tmux is in your PATH",
			"Verify installation: which tmux",
		},
	}
}

// ConfigurationError creates an error for configuration file issues
func ConfigurationError(configPath string, cause error) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Failed to load configuration from %s", configPath),
		Cause:   cause,
		Hints: []string{
			"Check YAML syntax: yaml lint " + configPath,
			"Verify file permissions: ls -l " + configPath,
			"See example config: examples/tmux-config-complete.yaml",
		},
	}
}

// AttachFailed creates an error for failed attach operations
func AttachFailed(sessionName string, cause error) *UserFriendlyError {
	return &UserFriendlyError{
		Message: fmt.Sprintf("Failed to attach to session '%s'", sessionName),
		Cause:   cause,
		Hints: []string{
			fmt.Sprintf("Check session status: opencode-tmux status %s", sessionName),
			"Verify session exists: tmux ls",
			"Check tmux server: tmux list-sessions",
		},
	}
}
