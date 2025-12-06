package internal

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// App represents the tmuxcoder application
type App struct {
	projectRoot string
	binPath     string

	serverCmd   *exec.Cmd
	serverLog   *os.File
	serverURL   string
	serverOwned bool
	cleanupOnce sync.Once
}

const (
	defaultAutoServerPort = "55306"
	defaultServerHost     = "127.0.0.1"
)

// NewApp creates a new App instance
func NewApp() *App {
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		fmt.Fprintf(os.Stderr, "Error: could not find project root\n")
		fmt.Fprintf(os.Stderr, "Please run from project directory or set TMUXCODER_ROOT\n")
		os.Exit(1)
	}

	return &App{
		projectRoot: projectRoot,
		binPath:     filepath.Join(projectRoot, "dist", "opencode-tmux"),
	}
}

// Close releases any background resources started by the app (e.g. auto-started server)
func (a *App) Close() {
	a.cleanupOnce.Do(func() {
		a.stopServer()
	})
}

// SmartStart implements zero-config startup with optional session name
func (a *App) SmartStart(sessionName string) error {
	// 1. Auto-select session name if not provided
	if sessionName == "" {
		sessionName = a.selectSessionName()
	}

	fmt.Printf("Starting session: %s\n", sessionName)

	// Ensure OpenCode server is running/available before touching opencode-tmux
	if err := a.ensureServer(); err != nil {
		return err
	}

	// 2. Check if session already exists and is running
	if a.isSessionRunning(sessionName) {
		fmt.Printf("Session '%s' is already running\n", sessionName)
		fmt.Printf("Attaching to existing session...\n")
		return a.attachToSession(sessionName, false)
	}

	// 3. Start daemon in background
	fmt.Printf("Starting daemon for session '%s'...\n", sessionName)
	if err := a.startDaemonBackground(sessionName); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// 4. Wait for session to be ready
	fmt.Printf("Waiting for session to be ready...\n")
	if err := a.waitForSessionReady(sessionName, 10); err != nil {
		return fmt.Errorf("session not ready: %w", err)
	}

	// 5. Attach to session
	fmt.Printf("Attaching to session '%s'...\n", sessionName)
	return a.attachToSession(sessionName, false)
}

// ListSessions lists all running sessions
func (a *App) ListSessions() error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	cmd := exec.Command(a.binPath, "list")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CreateSession creates a new session
func (a *App) CreateSession(args []string) error {
	sessionName := "opencode"
	if len(args) > 0 {
		sessionName = args[0]
	}

	return a.SmartStart(sessionName)
}

// AttachSession attaches to an existing session
func (a *App) AttachSession(args []string) error {
	sessionName := "opencode"
	if len(args) > 0 {
		sessionName = args[0]
	}

	return a.attachToSession(sessionName, false)
}

// StopSession stops a session daemon
func (a *App) StopSession(args []string) error {
	sessionName := "opencode"
	if len(args) > 0 {
		sessionName = args[0]
	}

	if err := a.ensureServer(); err != nil {
		return err
	}

	cmd := exec.Command(a.binPath, "stop", sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ShowStatus shows status of all sessions or a specific session
func (a *App) ShowStatus(args []string) error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	cmdArgs := []string{"status"}
	cmdArgs = append(cmdArgs, args...)

	if !containsPositionalArg(args) {
		if session := a.currentTmuxSession(); session != "" {
			cmdArgs = append(cmdArgs, session)
		} else if fallback := a.selectSessionName(); fallback != "" {
			cmdArgs = append(cmdArgs, fallback)
		}
	}

	cmd := exec.Command(a.binPath, cmdArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// PassThrough passes through to opencode-tmux for legacy compatibility
func (a *App) PassThrough(args []string) error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	cmd := exec.Command(a.binPath, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// --- Helper methods ---

// selectSessionName auto-selects a session name based on current directory
func (a *App) selectSessionName() string {
	// Try current directory name
	cwd, err := os.Getwd()
	if err == nil {
		basename := filepath.Base(cwd)
		if a.isValidSessionName(basename) {
			return basename
		}
	}

	// Fallback to default
	return "opencode"
}

func (a *App) currentTmuxSession() string {
	if os.Getenv("TMUX") == "" {
		return ""
	}

	cmd := exec.Command("tmux", "display-message", "-p", "#S")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func containsPositionalArg(args []string) bool {
	seenDoubleDash := false
	for _, arg := range args {
		if seenDoubleDash {
			return true
		}
		if arg == "--" {
			seenDoubleDash = true
			continue
		}
		if !strings.HasPrefix(arg, "-") {
			return true
		}
	}
	return false
}

// isValidSessionName checks if a name is valid for tmux session
func (a *App) isValidSessionName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	// Tmux doesn't allow : and . in session names
	if strings.ContainsAny(name, ":.") {
		return false
	}
	return true
}

// isSessionRunning checks if a session is currently running
func (a *App) isSessionRunning(sessionName string) bool {
	// Check if tmux session exists
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	if cmd.Run() != nil {
		return false
	}

	// Check if daemon is running (via status command)
	cmd = exec.Command(a.binPath, "status", "--json", sessionName)
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	var status struct {
		Status        string `json:"status"`
		DaemonRunning bool   `json:"daemon_running"`
	}
	if err := json.Unmarshal(output, &status); err == nil {
		return status.DaemonRunning && strings.EqualFold(status.Status, "Running")
	}

	// Fallback to legacy text parsing if JSON output isn't available
	return strings.Contains(string(output), "Orchestrator Daemon: ✓")
}

// startDaemonBackground starts a daemon in background
func (a *App) startDaemonBackground(sessionName string) error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	// Use opencode-tmux start with --daemon flag
	// The daemon will detach automatically
	cmd := exec.Command(a.binPath, "start", sessionName, "--daemon")

	// Start the process
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start daemon process: %w", err)
	}

	// Give the detached daemon a brief moment to initialize
	time.Sleep(500 * time.Millisecond)

	return nil
}

// ensureServer sets up OPENCODE_SERVER by reusing or starting the OpenCode server
func (a *App) ensureServer() error {
	if a.serverURL != "" {
		return nil
	}

	if server := os.Getenv("OPENCODE_SERVER"); server != "" {
		a.serverURL = server
		return nil
	}

	port := os.Getenv("OPENCODE_AUTO_SERVER_PORT")
	if port == "" {
		port = defaultAutoServerPort
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("invalid OPENCODE_AUTO_SERVER_PORT value %q", port)
	}

	url := fmt.Sprintf("http://%s:%s", defaultServerHost, port)
	if a.serverReachable(url, 500*time.Millisecond) {
		fmt.Printf("Reusing OpenCode server at %s\n", url)
		a.serverURL = url
		_ = os.Setenv("OPENCODE_SERVER", url)
		return nil
	}

	if err := a.startServerProcess(defaultServerHost, port, url); err != nil {
		return err
	}

	if err := a.waitForServerReady(url, 15*time.Second); err != nil {
		a.stopServer()
		return fmt.Errorf("failed to start OpenCode server: %w", err)
	}

	a.serverURL = url
	_ = os.Setenv("OPENCODE_SERVER", url)
	fmt.Printf("OpenCode server is ready at %s\n", url)
	return nil
}

func (a *App) startServerProcess(host, port, url string) error {
	if _, err := exec.LookPath("bun"); err != nil {
		return fmt.Errorf("bun is not installed (https://bun.sh) - required to start OpenCode server: %w", err)
	}

	serverDir := filepath.Join(a.projectRoot, "packages", "opencode", "packages", "opencode")
	if info, err := os.Stat(serverDir); err != nil || !info.IsDir() {
		return fmt.Errorf("opencode package not found at %s (run 'git submodule update --init packages/opencode')", serverDir)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to determine home directory: %w", err)
	}
	logDir := filepath.Join(homeDir, ".opencode")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", logDir, err)
	}

	logPath := filepath.Join(logDir, "opencode-server.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open server log: %w", err)
	}

	cmd := exec.Command("bun", "run", "src/index.ts", "serve", "--hostname", host, "--port", port)
	cmd.Dir = serverDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("failed to start OpenCode server: %w", err)
	}

	a.serverCmd = cmd
	a.serverLog = logFile
	a.serverOwned = true
	fmt.Printf("Starting OpenCode server at %s (logs: %s)\n", url, logPath)
	return nil
}

func (a *App) waitForServerReady(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.serverReachable(url, time.Second) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for OpenCode server at %s", url)
}

func (a *App) serverReachable(url string, timeout time.Duration) bool {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

func (a *App) stopServer() {
	if a.serverCmd == nil || !a.serverOwned {
		if a.serverLog != nil {
			a.serverLog.Close()
			a.serverLog = nil
		}
		return
	}

	done := make(chan struct{})
	go func() {
		_ = a.serverCmd.Wait()
		close(done)
	}()

	// Try graceful shutdown first
	if a.serverCmd.Process != nil {
		_ = a.serverCmd.Process.Signal(syscall.SIGTERM)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		if a.serverCmd.Process != nil {
			_ = a.serverCmd.Process.Kill()
		}
		<-done
	}

	if a.serverLog != nil {
		a.serverLog.Close()
		a.serverLog = nil
	}
	a.serverCmd = nil
	a.serverOwned = false
}

// waitForSessionReady waits for a tmux session to be ready
func (a *App) waitForSessionReady(sessionName string, timeoutSeconds int) error {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)

	for time.Now().Before(deadline) {
		// Check if tmux session exists
		cmd := exec.Command("tmux", "has-session", "-t", sessionName)
		if cmd.Run() == nil {
			// Session exists, wait a bit more for full initialization
			time.Sleep(500 * time.Millisecond)
			return nil
		}

		// Sleep before retry
		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for session '%s' to be ready", sessionName)
}

// attachToSession attaches to a tmux session
func (a *App) attachToSession(sessionName string, readOnly bool) error {
	args := []string{"attach-session", "-t", sessionName}
	if readOnly {
		args = append(args, "-r")
	}

	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// findProjectRoot tries to find the project root directory
func findProjectRoot() string {
	// Strategy 1: TMUXCODER_ROOT env var
	if root := os.Getenv("TMUXCODER_ROOT"); root != "" {
		if isProjectRoot(root) {
			return root
		}
	}

	// Strategy 2: Current working directory
	if cwd, err := os.Getwd(); err == nil {
		if isProjectRoot(cwd) {
			return cwd
		}
		if root := searchUpward(cwd); root != "" {
			return root
		}
	}

	// Strategy 3: Executable location
	if execPath, err := os.Executable(); err == nil {
		execPath, _ = filepath.EvalSymlinks(execPath)
		execDir := filepath.Dir(execPath)

		if isProjectRoot(execDir) {
			return execDir
		}

		if root := searchUpward(execDir); root != "" {
			return root
		}
	}

	return ""
}

// isProjectRoot checks if directory contains opencode-tmux binary
func isProjectRoot(dir string) bool {
	binPath := filepath.Join(dir, "dist", "opencode-tmux")
	_, err := os.Stat(binPath)
	return err == nil
}

// searchUpward searches upward for project root
func searchUpward(startDir string) string {
	dir := startDir
	for {
		if isProjectRoot(dir) {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
