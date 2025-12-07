package interfaces

import (
	"time"
)

// IpcRequester represents the identity of an IPC request sender
type IpcRequester struct {
	UID      uint32
	GID      uint32
	PID      int
	Username string
	Hostname string
}

// OrchestratorControl exposes control operations that can be invoked over IPC.
type OrchestratorControl interface {
	// ReloadLayout triggers the orchestrator to reload the tmux layout configuration
	// and apply it to the running session without restarting panel processes.
	ReloadLayout(configPath string) error

	// Shutdown triggers graceful shutdown of the orchestrator daemon.
	// If cleanup is true, the tmux session will also be destroyed.
	Shutdown(cleanup bool) error

	// GetStatus returns the current status of the session
	GetStatus() (*SessionStatus, error)

	// GetConnectedClients returns a list of connected tmux clients
	GetConnectedClients() ([]ClientInfo, error)

	// Ping checks if the daemon is responsive
	Ping() error
}

// SessionStatus represents the current status of a session
type SessionStatus struct {
	SessionName string        `json:"session_name"`
	DaemonPID   int           `json:"daemon_pid"`
	IsRunning   bool          `json:"is_running"`
	Uptime      time.Duration `json:"uptime"`
	StartedAt   time.Time     `json:"started_at"`
	ClientCount int           `json:"client_count"`
	Panels      []PanelStatus `json:"panels"`
	SocketPath  string        `json:"socket_path"`
	ConfigPath  string        `json:"config_path"`
	Owner       SessionOwner  `json:"owner"`
}

// PanelStatus represents the status of a single panel
type PanelStatus struct {
	Name      string `json:"name"`
	PaneID    string `json:"pane_id"`
	IsRunning bool   `json:"is_running"`
	PID       int    `json:"pid,omitempty"`
	Restarts  int    `json:"restarts"`
	LastError string `json:"last_error,omitempty"`
}

// ClientInfo represents information about a connected tmux client
type ClientInfo struct {
	TTY         string    `json:"tty"`
	PID         int       `json:"pid"`
	ConnectedAt time.Time `json:"connected_at"`
	SessionName string    `json:"session_name"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
}

// SessionOwner represents the owner of a session
type SessionOwner struct {
	UID       uint32    `json:"uid"`
	GID       uint32    `json:"gid"`
	Username  string    `json:"username"`
	Hostname  string    `json:"hostname"`
	StartedAt time.Time `json:"started_at"`
}
