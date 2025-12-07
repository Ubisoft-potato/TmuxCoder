package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete configuration for opencode-tmux
type Config struct {
	Supervision SupervisionConfig `yaml:"supervision"`
	IPC         IPCConfig         `yaml:"ipc"`
	Permissions PermissionsConfig `yaml:"permissions"`
}

// SupervisionConfig controls process monitoring and health checking
type SupervisionConfig struct {
	Enabled             bool          `yaml:"enabled"`               // Whether supervision is enabled
	HealthCheckInterval time.Duration `yaml:"health_check_interval"` // How often to check pane health
	RestartDelay        time.Duration `yaml:"restart_delay"`         // Initial delay before restarting failed process
	MaxRestartDelay     time.Duration `yaml:"max_restart_delay"`     // Maximum delay between restart attempts
}

// IPCConfig controls IPC socket behavior
type IPCConfig struct {
	SocketDir  string        `yaml:"socket_dir"`  // Directory for IPC socket files
	SocketMode string        `yaml:"socket_mode"` // Unix file permissions (e.g., "0600")
	Timeout    time.Duration `yaml:"timeout"`     // IPC request timeout
}

// PermissionsConfig controls who can perform various operations
type PermissionsConfig struct {
	Shutdown     string `yaml:"shutdown"`      // Who can shutdown: "owner", "group", "any"
	ReloadLayout string `yaml:"reload_layout"` // Who can reload layout
	Status       string `yaml:"status"`        // Who can view status
}

// DefaultConfig returns the default configuration
func DefaultConfig() *Config {
	return &Config{
		Supervision: SupervisionConfig{
			Enabled:             true,
			HealthCheckInterval: 2 * time.Second,
			RestartDelay:        1 * time.Second,
			MaxRestartDelay:     30 * time.Second,
		},
		IPC: IPCConfig{
			SocketDir:  "/tmp/opencode-tmux",
			SocketMode: "0600",
			Timeout:    10 * time.Second,
		},
		Permissions: PermissionsConfig{
			Shutdown:     "owner",
			ReloadLayout: "owner",
			Status:       "any",
		},
	}
}

// LoadConfig loads configuration from a YAML file
// If the file doesn't exist or has errors, returns default config with error
func LoadConfig(path string) (*Config, error) {
	// Read the file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// Start with default config, then overlay file values
	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return cfg, nil
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	// Validate supervision config
	if c.Supervision.Enabled {
		if c.Supervision.HealthCheckInterval < time.Second {
			return fmt.Errorf("health_check_interval must be >= 1s, got %v", c.Supervision.HealthCheckInterval)
		}
		if c.Supervision.RestartDelay < 0 {
			return fmt.Errorf("restart_delay cannot be negative, got %v", c.Supervision.RestartDelay)
		}
		if c.Supervision.MaxRestartDelay < c.Supervision.RestartDelay {
			return fmt.Errorf("max_restart_delay (%v) must be >= restart_delay (%v)",
				c.Supervision.MaxRestartDelay, c.Supervision.RestartDelay)
		}
	}

	// Validate IPC config
	if c.IPC.SocketDir == "" {
		return fmt.Errorf("ipc.socket_dir cannot be empty")
	}
	if c.IPC.Timeout < 0 {
		return fmt.Errorf("ipc.timeout cannot be negative, got %v", c.IPC.Timeout)
	}

	// Validate permissions
	validPerms := map[string]bool{"owner": true, "group": true, "any": true}
	if !validPerms[c.Permissions.Shutdown] {
		return fmt.Errorf("invalid shutdown permission: %s (must be 'owner', 'group', or 'any')",
			c.Permissions.Shutdown)
	}
	if !validPerms[c.Permissions.ReloadLayout] {
		return fmt.Errorf("invalid reload_layout permission: %s (must be 'owner', 'group', or 'any')",
			c.Permissions.ReloadLayout)
	}
	if !validPerms[c.Permissions.Status] {
		return fmt.Errorf("invalid status permission: %s (must be 'owner', 'group', or 'any')",
			c.Permissions.Status)
	}

	return nil
}

// LoadOrDefault attempts to load config from path, falling back to defaults on error
// This is the recommended way to load config in production code
func LoadOrDefault(path string) *Config {
	if path == "" {
		return DefaultConfig()
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		// Log warning but continue with defaults
		fmt.Fprintf(os.Stderr, "Warning: failed to load config from %s: %v\n", path, err)
		fmt.Fprintf(os.Stderr, "Using default configuration\n")
		return DefaultConfig()
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: invalid config in %s: %v\n", path, err)
		fmt.Fprintf(os.Stderr, "Using default configuration\n")
		return DefaultConfig()
	}

	return cfg
}
