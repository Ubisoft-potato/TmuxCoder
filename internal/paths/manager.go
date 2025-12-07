package paths

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// PathManager manages all session-related file paths
type PathManager struct {
	sessionName string
	baseDir     string
}

// NewPathManager creates a new path manager
func NewPathManager(sessionName string) *PathManager {
	// Use ~/.opencode instead of temp directory for better multi-user support
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// Fallback to temp dir if home dir is not available
		homeDir = os.TempDir()
	}

	return &PathManager{
		sessionName: sessionName,
		baseDir:     filepath.Join(homeDir, ".opencode"),
	}
}

// SocketPath returns the IPC socket path
func (p *PathManager) SocketPath() string {
	return filepath.Join(p.baseDir, "sockets", p.sessionName+".sock")
}

// StatePath returns the state file path
func (p *PathManager) StatePath() string {
	return filepath.Join(p.baseDir, "states", p.sessionName+".json")
}

// LogPath returns the log file path
func (p *PathManager) LogPath() string {
	return filepath.Join(p.baseDir, "logs", p.sessionName+".log")
}

// PIDPath returns the PID file path
func (p *PathManager) PIDPath() string {
	return filepath.Join(p.baseDir, "locks", p.sessionName+".pid")
}

// BackupPath returns the backup file path
func (p *PathManager) BackupPath(generation int) string {
	statePath := p.StatePath()
	if generation == 0 {
		return statePath + ".backup"
	}
	return fmt.Sprintf("%s.backup.%d", statePath, generation)
}

// EnsureDirectories ensures all necessary directories exist
func (p *PathManager) EnsureDirectories() error {
	dirs := []string{
		filepath.Join(p.baseDir, "sockets"),
		filepath.Join(p.baseDir, "states"),
		filepath.Join(p.baseDir, "logs"),
		filepath.Join(p.baseDir, "locks"),
		filepath.Join(p.baseDir, "cache"),
	}

	for _, dir := range dirs {
		// Use 0755 for base directory creation
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// For sockets directory, ensure it's accessible to all users for testing permissions
		// This allows other users to connect to the socket (file-level permissions control access)
		if strings.HasSuffix(dir, "sockets") {
			if err := os.Chmod(dir, 0777); err != nil {
				return fmt.Errorf("failed to set permissions on sockets directory %s: %w", dir, err)
			}
		}
	}

	return nil
}

// Cleanup cleans up all files for this session
func (p *PathManager) Cleanup() error {
	files := []string{
		p.SocketPath(),
		p.PIDPath(),
	}

	for _, file := range files {
		os.Remove(file) // Ignore errors
	}

	return nil
}

// CleanupStaleFiles cleans up all stale files
func (p *PathManager) CleanupStaleFiles(maxAge time.Duration) error {
	// Check all PID files
	pidFiles, err := filepath.Glob(filepath.Join(p.baseDir, "locks", "*.pid"))
	if err != nil {
		return err
	}

	for _, pidFile := range pidFiles {
		if isStale, _ := p.isPIDFileStale(pidFile); isStale {
			sessionName := strings.TrimSuffix(filepath.Base(pidFile), ".pid")

			// Clean up related files
			os.Remove(pidFile)
			os.Remove(filepath.Join(p.baseDir, "sockets", sessionName+".sock"))
		}
	}

	return nil
}

func (p *PathManager) isPIDFileStale(pidFile string) (bool, error) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return true, err
	}

	var pid int
	fmt.Sscanf(string(data), "%d", &pid)

	// Check if process exists
	process, err := os.FindProcess(pid)
	if err != nil {
		return true, err
	}

	// Send signal 0 to detect
	err = process.Signal(syscall.Signal(0))
	return err != nil, nil
}
