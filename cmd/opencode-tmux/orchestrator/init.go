package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/opencode/tmux_coder/cmd/opencode-tmux/config"
	"github.com/opencode/tmux_coder/internal/session"
	"github.com/opencode/tmux_coder/internal/theme"
	"github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
)

// InitResult holds the result of orchestrator initialization
type InitResult struct {
	Orchestrator interface{} // Will be *TmuxOrchestrator from main package
	Lock         *session.SessionLock
	ShouldAttach bool
	Error        error
}

// Initialize creates and initializes a new orchestrator instance
// This function extracts the initialization logic from runLegacyMode
func Initialize(ctx context.Context, cfg *config.OrchestratorConfig) *InitResult {
	result := &InitResult{}

	// Phase 1: Validate configuration
	if err := cfg.Validate(); err != nil {
		result.Error = fmt.Errorf("invalid configuration: %w", err)
		return result
	}

	// Phase 2: Setup logging
	if err := setupLogging(cfg); err != nil {
		log.Printf("Warning: failed to setup logging: %v", err)
	}

	// Phase 3: Ensure paths are set
	if err := cfg.EnsurePaths(); err != nil {
		result.Error = fmt.Errorf("failed to ensure paths: %w", err)
		return result
	}

	// Phase 4: Handle reload-layout command (if needed)
	// This is handled separately before lock acquisition in runLegacyMode
	// We skip it here as it should be checked before calling Initialize()

	// Phase 5: Acquire session lock
	lock, err := acquireSessionLock(cfg)
	if err != nil {
		result.Error = fmt.Errorf("failed to acquire lock: %w", err)
		return result
	}
	result.Lock = lock

	// Phase 6: Load layout configuration
	if err := cfg.LoadLayoutFromPath(); err != nil {
		lock.Release()
		result.Error = fmt.Errorf("failed to load layout: %w", err)
		return result
	}

	// Phase 7: Initialize theme
	if err := initializeTheme(); err != nil {
		lock.Release()
		result.Error = fmt.Errorf("failed to initialize theme: %w", err)
		return result
	}

	// Phase 8: Setup tmux session (determine if we should attach)
	shouldAttach, err := setupTmuxSession(cfg)
	if err != nil {
		lock.Release()
		result.Error = fmt.Errorf("failed to setup tmux: %w", err)
		return result
	}
	result.ShouldAttach = shouldAttach

	// Note: The actual orchestrator creation and panel spawning
	// is still done in main.go using NewTmuxOrchestrator and orchestrator.Start()
	// This will be refactored in a later phase to fully extract the logic

	return result
}

// setupLogging configures logging to file
func setupLogging(cfg *config.OrchestratorConfig) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	logDir := filepath.Join(homeDir, ".opencode")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	logPath := filepath.Join(logDir, fmt.Sprintf("tmux-%s.log", sanitizeLogComponent(cfg.SessionName)))
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	return nil
}

// sanitizeLogComponent sanitizes session name for use in log file name
func sanitizeLogComponent(s string) string {
	// Replace any characters that are not alphanumeric, dash, or underscore with underscore
	result := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			result = append(result, c)
		} else {
			result = append(result, '_')
		}
	}
	return string(result)
}

// acquireSessionLock tries to acquire lock for the session
func acquireSessionLock(cfg *config.OrchestratorConfig) (*session.SessionLock, error) {
	// Check if another instance is already running
	if pid, running := session.CheckLock(cfg.PIDPath); running {
		if cfg.ForceNewSession {
			log.Printf("Force flag set, killing existing process (PID: %d)...", pid)
			if err := session.StopProcess(pid); err != nil {
				return nil, fmt.Errorf("failed to stop existing process: %w", err)
			}
			// Remove stale lock file
			os.Remove(cfg.PIDPath)
		} else if cfg.ReuseExisting {
			// For reuse, we don't fail - we'll reuse the existing session
			log.Printf("Reusing existing session (daemon already running)")
			// Return a dummy lock that won't actually lock anything
			return &session.SessionLock{}, nil
		} else {
			return nil, fmt.Errorf("orchestrator already running for session '%s' (PID: %d)\nPID file: %s",
				cfg.SessionName, pid, cfg.PIDPath)
		}
	}

	// Acquire lock
	lock, err := session.AcquireLock(cfg.PIDPath)
	if err != nil {
		return nil, fmt.Errorf("failed to acquire lock: %w", err)
	}

	log.Printf("Lock acquired: %s", cfg.PIDPath)
	return lock, nil
}

// initializeTheme loads and sets the theme
func initializeTheme() error {
	if err := theme.LoadThemesFromJSON(); err != nil {
		return fmt.Errorf("failed to load themes: %w", err)
	}
	if err := theme.SetTheme("opencode"); err != nil {
		return fmt.Errorf("failed to set theme: %w", err)
	}
	return nil
}

// setupTmuxSession creates or validates tmux session
// Returns whether we should attach to the session
func setupTmuxSession(cfg *config.OrchestratorConfig) (shouldAttach bool, err error) {
	// Handle attach-only mode
	if cfg.AttachOnly {
		// In attach-only mode, we don't manage the session, just attach
		return true, nil
	}

	// In server-only or daemon mode, we don't attach
	if cfg.ServerOnly || cfg.Daemon {
		return false, nil
	}

	// Default: attach if stdin is a terminal
	return isTerminal(), nil
}

// isTerminal checks if stdin is a terminal
func isTerminal() bool {
	fileInfo, _ := os.Stdin.Stat()
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// CreateHTTPClient creates an HTTP client for OpenCode API
func CreateHTTPClient(serverURL, apiKey string) *opencode.Client {
	if serverURL == "" {
		return nil
	}

	// API key is handled via environment variable by the SDK
	// Just set the base URL
	return opencode.NewClient(option.WithBaseURL(serverURL))
}

// CleanupStaleFiles removes stale files from previous runs
func CleanupStaleFiles(cfg *config.OrchestratorConfig) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Clean up socket and state files older than 7 days
	staleAge := 7 * 24 * time.Hour
	now := time.Now()

	cleanupFile := func(path string) {
		info, err := os.Stat(path)
		if err != nil {
			return // File doesn't exist or can't be accessed
		}

		if now.Sub(info.ModTime()) > staleAge {
			log.Printf("Removing stale file: %s", path)
			os.Remove(path)
		}
	}

	// Check common paths
	opencodDir := filepath.Join(homeDir, ".opencode")
	entries, err := os.ReadDir(opencodDir)
	if err != nil {
		return nil // Directory doesn't exist, nothing to clean
	}

	for _, entry := range entries {
		path := filepath.Join(opencodDir, entry.Name())
		cleanupFile(path)
	}

	return nil
}
