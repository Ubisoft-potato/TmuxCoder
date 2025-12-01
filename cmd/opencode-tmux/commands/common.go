package commands

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/opencode/tmux_coder/internal/paths"
)

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// getSessionName returns session name from args or default
func getSessionName(args []string) string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0]
	}
	return "opencode"
}

// sessionExists checks if a tmux session exists
func sessionExists(sessionName string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	return cmd.Run() == nil
}

// getSocketPath returns the IPC socket path for a session
func getSocketPath(sessionName string) string {
	pathMgr := paths.NewPathManager(sessionName)
	return pathMgr.SocketPath()
}

// getPIDPath returns the PID file path for a session
func getPIDPath(sessionName string) string {
	pathMgr := paths.NewPathManager(sessionName)
	return pathMgr.PIDPath()
}

// isSocketActive checks if socket is active (daemon running)
func isSocketActive(socketPath string) bool {
	// Check if socket file exists
	info, err := os.Stat(socketPath)
	if err != nil {
		return false
	}

	// Check if it's a socket file
	if info.Mode()&os.ModeSocket == 0 {
		return false
	}

	// Try connecting to check if process is listening
	conn, err := net.DialTimeout("unix", socketPath, 1*time.Second)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

// isDaemonRunning checks if daemon is running for a session
func isDaemonRunning(sessionName string) bool {
	socketPath := getSocketPath(sessionName)
	return isSocketActive(socketPath)
}

// inTmuxSession checks if currently inside a tmux session
func inTmuxSession() bool {
	return os.Getenv("TMUX") != ""
}

// getCurrentTmuxSession returns current tmux session name
func getCurrentTmuxSession() string {
	if !inTmuxSession() {
		return ""
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#S")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

// getConnectedClientsCount returns number of connected clients
func getConnectedClientsCount(sessionName string) (int, error) {
	cmd := exec.Command("tmux", "list-clients", "-t", sessionName)
	output, err := cmd.Output()
	if err != nil {
		return 0, err
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

// ClientInfo represents information about a connected client
type ClientInfo struct {
	TTY         string
	PID         string
	ConnectedAt string
}

// getConnectedClients returns list of connected clients with details
func getConnectedClients(sessionName string) ([]ClientInfo, error) {
	cmd := exec.Command("tmux", "list-clients", "-t", sessionName, "-F", "#{client_tty} #{client_pid} #{client_created}")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var clients []ClientInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
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
				ConnectedAt: parts[2],
			})
		}
	}

	return clients, nil
}

// ensureOpenCodeDir creates opencode-tmux directory if not exists
func ensureOpenCodeDir() error {
	openCodeDir := filepath.Join(os.TempDir(), "opencode-tmux")
	return os.MkdirAll(openCodeDir, 0755)
}

// getOpenCodeDir returns opencode-tmux directory path
func getOpenCodeDir() (string, error) {
	return filepath.Join(os.TempDir(), "opencode-tmux"), nil
}

// findAllSessions scans /tmp/opencode-tmux for all sessions
func findAllSessions() ([]string, error) {
	baseDir := filepath.Join(os.TempDir(), "opencode-tmux")

	// Check sockets directory for active sessions
	socketsDir := filepath.Join(baseDir, "sockets")
	socketFiles, err := filepath.Glob(filepath.Join(socketsDir, "*.sock"))
	if err != nil {
		return nil, err
	}

	sessionMap := make(map[string]bool)

	// Extract session names from socket files
	for _, socketPath := range socketFiles {
		basename := filepath.Base(socketPath)
		sessionName := strings.TrimSuffix(basename, ".sock")
		sessionMap[sessionName] = true
	}

	// Also check locks directory for PID files
	locksDir := filepath.Join(baseDir, "locks")
	pidFiles, err := filepath.Glob(filepath.Join(locksDir, "*.pid"))
	if err == nil {
		for _, pidPath := range pidFiles {
			basename := filepath.Base(pidPath)
			sessionName := strings.TrimSuffix(basename, ".pid")
			sessionMap[sessionName] = true
		}
	}

	// Also check tmux sessions to find orphaned sessions
	tmuxSessions, err := getTmuxSessions()
	if err == nil {
		for _, tmuxSession := range tmuxSessions {
			sessionMap[tmuxSession] = true
		}
	}

	// Convert map to slice
	var sessions []string
	for session := range sessionMap {
		sessions = append(sessions, session)
	}

	return sessions, nil
}

// getTmuxSessions returns a list of all tmux sessions
func getTmuxSessions() ([]string, error) {
	cmd := exec.Command("tmux", "list-sessions", "-F", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		// tmux returns error if no sessions exist, which is not a real error
		return nil, nil
	}

	var sessions []string
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}

	return sessions, nil
}

// SessionStatus represents the status of a session
type SessionStatus struct {
	Name          string
	TmuxRunning   bool
	DaemonRunning bool
	ClientCount   int
}

// checkSessionStatus checks the status of a session
func checkSessionStatus(sessionName string) SessionStatus {
	status := SessionStatus{
		Name:          sessionName,
		TmuxRunning:   sessionExists(sessionName),
		DaemonRunning: isDaemonRunning(sessionName),
		ClientCount:   0,
	}

	if status.TmuxRunning {
		count, err := getConnectedClientsCount(sessionName)
		if err == nil {
			status.ClientCount = count
		}
	}

	return status
}
