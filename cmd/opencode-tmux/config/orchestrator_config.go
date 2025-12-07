package config

import (
	"fmt"
	"os"
	"path/filepath"

	tmuxconfig "github.com/opencode/tmux_coder/internal/config"
	"github.com/opencode/tmux_coder/internal/paths"
)

// OrchestratorConfig holds all configuration for starting an orchestrator
type OrchestratorConfig struct {
	// Session configuration
	SessionName string
	ServerURL   string
	APIKey      string

	// Behavior flags
	ServerOnly      bool // Start daemon without attaching
	AttachOnly      bool // Attach to existing session only
	ReuseExisting   bool // Reuse existing tmux session
	ForceNewSession bool // Kill existing and create new
	Daemon          bool // Run in daemon mode
	ReloadLayout    bool // Reload layout without restarting panels

	// Paths
	ConfigPath string
	SocketPath string
	StatePath  string
	PIDPath    string
	LogPath    string

	// Layout
	Layout     *tmuxconfig.Layout
	LayoutPath string

	// Client configuration
	ClientType string // "tmux", "ssh", etc.
	ClientID   string
	TTY        string

	// Advanced options
	NoAutoStart bool   // Don't start panels automatically
	DetachKeys  string // Custom detach key sequence
	AttachRead  bool   // Attach in read-only mode
}

// Validate checks if configuration is valid
func (c *OrchestratorConfig) Validate() error {
	if c.SessionName == "" {
		return fmt.Errorf("session name is required")
	}
	if c.ServerURL == "" && !c.ServerOnly {
		return fmt.Errorf("server URL is required (set OPENCODE_SERVER environment variable)")
	}
	return nil
}

// LoadLayoutFromPath loads layout configuration from file
func (c *OrchestratorConfig) LoadLayoutFromPath() error {
	configPath := c.ConfigPath
	if configPath == "" {
		// Use default config path
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		configPath = filepath.Join(homeDir, ".opencode", "tmux.yaml")
	}

	// If a specific layout path is provided, use it
	if c.LayoutPath != "" {
		configPath = c.LayoutPath
	}

	// Load layout configuration
	layout, err := tmuxconfig.LoadLayout(configPath)
	if err != nil {
		return fmt.Errorf("failed to load layout config: %w", err)
	}

	c.Layout = layout
	c.ConfigPath = configPath
	return nil
}

// EnsureClientID generates a client ID if not provided
func (c *OrchestratorConfig) EnsureClientID() {
	if c.ClientID == "" {
		c.ClientID = fmt.Sprintf("%s-%d", c.ClientType, os.Getpid())
	}

	if c.TTY == "" {
		c.TTY = os.Getenv("TTY")
		if c.TTY == "" {
			c.TTY = "/dev/pts/unknown"
		}
	}
}

// EnsurePaths ensures all paths are set, using defaults if needed
func (c *OrchestratorConfig) EnsurePaths() error {
	pathMgr := paths.NewPathManager(c.SessionName)

	// Ensure directories exist
	if err := pathMgr.EnsureDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Set paths from environment or use path manager defaults
	if envSocket := os.Getenv("OPENCODE_SOCKET"); envSocket != "" {
		c.SocketPath = envSocket
	} else {
		c.SocketPath = pathMgr.SocketPath()
	}

	if envState := os.Getenv("OPENCODE_STATE"); envState != "" {
		c.StatePath = envState
	} else {
		c.StatePath = pathMgr.StatePath()
	}

	c.PIDPath = pathMgr.PIDPath()
	c.LogPath = pathMgr.LogPath()

	return nil
}
