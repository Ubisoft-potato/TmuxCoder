package internal

import (
	"bufio"
	"bytes"
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

	layoutOverride string
	layoutApplied  bool

	mergeTarget string
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

	binPath := filepath.Join(projectRoot, "cmd", "opencode-tmux", "dist", "opencode-tmux")

	// Check if the opencode-tmux binary exists
	if _, err := os.Stat(binPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: opencode-tmux binary not found at: %s\n", binPath)
		fmt.Fprintf(os.Stderr, "Please run 'make build' to compile the binaries\n")
		os.Exit(1)
	}

	// Propagate project metadata to child processes
	if os.Getenv("TMUXCODER_ROOT") == "" {
		_ = os.Setenv("TMUXCODER_ROOT", projectRoot)
	}
	configDir := filepath.Join(projectRoot, ".opencode")
	if os.Getenv("OPENCODE_CONFIG_DIR") == "" {
		if info, err := os.Stat(configDir); err == nil && info.IsDir() {
			_ = os.Setenv("OPENCODE_CONFIG_DIR", configDir)
		}
	}

	return &App{
		projectRoot:    projectRoot,
		binPath:        binPath,
		layoutOverride: os.Getenv("TMUXCODER_LAYOUT_OVERRIDE_PATH"),
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

	// 2. Prompt merge behavior
	insideTmux := os.Getenv("TMUX") != ""
	if insideTmux {
		if cur := a.currentTmuxSession(); cur != "" {
			choice := promptChoice(fmt.Sprintf("Detected tmux session '%s'. Merge panes into this session?", cur), []string{"merge", "parallel", "cancel"}, "merge")
			if choice == "cancel" {
				return fmt.Errorf("user canceled")
			}
			if choice == "merge" {
				a.mergeTarget = cur
			}
		}
	} else {
		sessions := listTmuxSessions()
		if len(sessions) > 0 {
			def := "parallel"
			if len(sessions) == 1 {
				def = "merge"
			}
			choice := promptChoice("Found existing tmux session(s). Merge into one?", []string{"merge", "parallel", "cancel"}, def)
			if choice == "cancel" {
				return fmt.Errorf("user canceled")
			}
			if choice == "merge" {
				target := sessions[0]
				if len(sessions) > 1 {
					target = promptSelect("Select tmux session to merge into", sessions, sessions[0])
				}
				a.mergeTarget = target
			}
		}
	}

	// If session already running, attach (or switch) to the right tmux session.
	if a.isSessionRunning(sessionName) {
		if a.mergeTarget == "" {
			if err := a.maybeApplyLayoutOverride(sessionName); err != nil {
				return err
			}
			fmt.Printf("Session '%s' is already running\n", sessionName)
			fmt.Printf("Attaching to existing session...\n")
			return a.attachToSession(sessionName, false)
		}

		fmt.Printf("Daemon for '%s' is already running\n", sessionName)
		if insideTmux {
			fmt.Printf("Merged into tmux session '%s'. Switch there to view panels.\n", a.mergeTarget)
			return nil
		}
		fmt.Printf("Attaching to tmux session '%s'...\n", a.mergeTarget)
		return a.attachToSession(a.mergeTarget, false)
	}

	// 3. Start daemon in background
	fmt.Printf("Starting daemon for session '%s'...\n", sessionName)
	if err := a.startDaemonBackground(sessionName); err != nil {
		return fmt.Errorf("failed to start daemon: %w", err)
	}

	// 4. Wait for readiness
	if a.mergeTarget == "" {
		fmt.Printf("Waiting for session to be ready...\n")
		if err := a.waitForSessionReady(sessionName, 10); err != nil {
			return fmt.Errorf("session not ready: %w", err)
		}
	} else {
		fmt.Printf("Waiting for daemon to be ready...\n")
		if err := a.waitForDaemonReady(sessionName, 15); err != nil {
			return fmt.Errorf("daemon not ready: %w", err)
		}
	}

	// 5. Attach (or stay)
	if a.mergeTarget != "" {
		if insideTmux {
			fmt.Printf("Merged into tmux session '%s'. Switch there to view panels.\n", a.mergeTarget)
			return nil
		}
		fmt.Printf("Attaching to tmux session '%s'...\n", a.mergeTarget)
		return a.attachToSession(a.mergeTarget, false)
	}

	fmt.Printf("Attaching to session '%s'...\n", sessionName)
	return a.attachToSession(sessionName, false)
}

// ListSessions lists all running sessions
func (a *App) ListSessions() error {
	cmd := exec.Command(a.binPath, "list")
	cmd.Env = os.Environ()
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

	if a.isSessionRunning(sessionName) {
		if err := a.maybeApplyLayoutOverride(sessionName); err != nil {
			return err
		}
	}

	return a.attachToSession(sessionName, false)
}

// StopSession stops a session daemon
func (a *App) StopSession(args []string) error {
	sessionName := "opencode"
	cleanup := false

	// Parse arguments
	for i, arg := range args {
		if arg == "--cleanup" || arg == "-c" {
			cleanup = true
		} else if !strings.HasPrefix(arg, "-") && i == 0 {
			// First non-flag argument is session name
			sessionName = arg
		}
	}

	// Build command args
	// Note: opencode-tmux expects: stop --cleanup <session-name>
	cmdArgs := []string{"stop"}
	if cleanup {
		cmdArgs = append(cmdArgs, "--cleanup")
	}
	cmdArgs = append(cmdArgs, sessionName)

	cmd := exec.Command(a.binPath, cmdArgs...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ShowStatus shows status of all sessions or a specific session
func (a *App) ShowStatus(args []string) error {
	// If no session name provided, list all sessions instead
	if len(args) == 0 || (len(args) > 0 && strings.HasPrefix(args[0], "-")) {
		fmt.Println("Available sessions:")
		fmt.Println("")
		return a.ListSessions()
	}

	sessionName := args[0]
	cmd := exec.Command(a.binPath, "status", sessionName)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// ReloadLayout applies a new layout config to an existing session without attaching
func (a *App) ReloadLayout(sessionName, layoutPath string) error {
	if sessionName == "" {
		return fmt.Errorf("session name is required for layout reload")
	}

	resolved, err := resolveLayoutPath(layoutPath)
	if err != nil {
		return err
	}
	if info, err := os.Stat(resolved); err != nil {
		return fmt.Errorf("layout file not accessible: %w", err)
	} else if info.IsDir() {
		return fmt.Errorf("layout path %s is a directory", resolved)
	}

	if !a.isSessionRunning(sessionName) {
		return fmt.Errorf("session '%s' is not running (start it first)", sessionName)
	}

	fmt.Printf("Reloading layout for session '%s' using %s...\n", sessionName, resolved)

	cmd := exec.Command(a.binPath, "--reload-layout", sessionName)
	env := append([]string{}, os.Environ()...)
	env = append(env,
		"OPENCODE_TMUX_CONFIG="+resolved,
		"TMUXCODER_LAYOUT_OVERRIDE_PATH="+resolved,
	)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload layout: %w", err)
	}

	return nil
}

// PassThrough passes through to opencode-tmux for legacy compatibility
func (a *App) PassThrough(args []string) error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	finalArgs := append([]string{}, args...)
	if a.mergeTarget != "" && !containsFlag(finalArgs, "--merge-into") {
		finalArgs = append([]string{"--merge-into=" + a.mergeTarget}, finalArgs...)
	}
	cmd := exec.Command(a.binPath, finalArgs...)
	cmd.Env = os.Environ()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout

	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	if err == nil {
		if stderrBuf.Len() > 0 {
			_, _ = os.Stderr.Write(stderrBuf.Bytes())
		}
		return nil
	}

	if userErr := a.sanitizePassThroughError(args, stderrBuf.String()); userErr != nil {
		return userErr
	}

	if stderrBuf.Len() > 0 {
		_, _ = os.Stderr.Write(stderrBuf.Bytes())
	}

	return err
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
	// In merge mode, the orchestrator session name does not correspond to a tmux session.
	// Only the daemon being alive matters.
	if a.mergeTarget == "" {
		cmd := exec.Command("tmux", "has-session", "-t", sessionName)
		if cmd.Run() != nil {
			return false
		}
	}

	// Check if daemon is running (via status command)
	cmd := exec.Command(a.binPath, "status", "--json", sessionName)
	cmd.Env = os.Environ()
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	var status struct {
		Status        string `json:"status"`
		DaemonRunning bool   `json:"daemon_running"`
	}
	if err := json.Unmarshal(output, &status); err == nil {
		if a.mergeTarget != "" {
			return status.DaemonRunning
		}
		return status.DaemonRunning && strings.EqualFold(status.Status, "Running")
	}

	// Fallback to legacy text parsing if JSON output isn't available
	return strings.Contains(string(output), "Orchestrator Daemon: ✓")
}

func (a *App) sanitizePassThroughError(args []string, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		return nil
	}

	lower := strings.ToLower(msg)
	if strings.Contains(lower, "flag provided but not defined") ||
		strings.Contains(lower, "unknown shorthand flag") ||
		strings.Contains(lower, "unknown flag") {
		flagName := extractFlagName(msg)
		if flagName == "" && len(args) > 0 {
			flagName = args[0]
		}
		if flagName == "" {
			flagName = "specified option"
		}
		return fmt.Errorf("unrecognized option %s (run 'tmuxcoder help' for usage)", flagName)
	}

	if strings.Contains(lower, "usage of") && strings.Contains(lower, "opencode-tmux") {
		return fmt.Errorf("invalid tmuxcoder arguments (run 'tmuxcoder help')")
	}

	return nil
}

func extractFlagName(msg string) string {
	patterns := []string{
		"flag provided but not defined:",
		"unknown shorthand flag:",
		"unknown flag:",
	}

	for _, pattern := range patterns {
		if idx := strings.Index(msg, pattern); idx >= 0 {
			remainder := strings.TrimSpace(msg[idx+len(pattern):])
			if remainder == "" {
				continue
			}

			// Handle formats like "'s' in -server"
			if strings.Contains(remainder, " in ") {
				if parts := strings.SplitN(remainder, " in ", 2); len(parts) == 2 {
					fields := strings.Fields(parts[1])
					if len(fields) > 0 {
						return fields[0]
					}
				}
			}

			fields := strings.Fields(remainder)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}

	return ""
}

func (a *App) maybeApplyLayoutOverride(sessionName string) error {
	if a.layoutOverride == "" || a.layoutApplied {
		return nil
	}

	fmt.Printf("Reloading layout for session '%s' using %s...\n", sessionName, a.layoutOverride)

	cmd := exec.Command(a.binPath, "--reload-layout", sessionName)
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload layout: %w", err)
	}

	a.layoutApplied = true
	return nil
}

// startDaemonBackground starts a daemon in background
func (a *App) startDaemonBackground(sessionName string) error {
	if err := a.ensureServer(); err != nil {
		return err
	}
	// Use opencode-tmux start with --daemon flag
	// The daemon will detach automatically
	cmdArgs := []string{"start", sessionName, "--daemon"}
	if a.mergeTarget != "" {
		cmdArgs = append(cmdArgs, "--merge-into", a.mergeTarget)
	}

	// Pass prompt configuration flags
	if customSP := os.Getenv("TMUXCODER_CUSTOM_SP"); customSP != "" {
		cmdArgs = append(cmdArgs, "--custom-sp", customSP)
	}
	if cleanDefaultEnvSP := os.Getenv("TMUXCODER_CLEAN_DEFAULT_ENV_SP"); cleanDefaultEnvSP != "" {
		cmdArgs = append(cmdArgs, "--clean-default-env-sp", cleanDefaultEnvSP)
	}

	cmd := exec.Command(a.binPath, cmdArgs...)
	cmd.Env = os.Environ() // Inherit environment variables including OPENCODE_SERVER

	// Capture output to surface errors to the user
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Start the process
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(stdout.String())
		errOut := strings.TrimSpace(stderr.String())
		if out != "" || errOut != "" {
			return fmt.Errorf("failed to start daemon process: %w\n%s\n%s", err, out, errOut)
		}
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

	// Check if server is already running
	serverRunning := a.serverReachable(url, 500*time.Millisecond)

	// Check if force restart is needed
	forceRestart := os.Getenv("TMUXCODER_FORCE_RESTART_SERVER") == "true"
	hasPromptConfig := os.Getenv("TMUXCODER_CUSTOM_SP") != "" || os.Getenv("TMUXCODER_CLEAN_DEFAULT_ENV_SP") != ""

	if serverRunning {
		// If force restart is requested with prompt config
		if forceRestart && hasPromptConfig {
			fmt.Println("Force restarting OpenCode server to apply new prompt configuration...")

			// Try to stop existing server by killing the bun process
			if err := a.killExistingServer(port); err != nil {
				fmt.Printf("Warning: Failed to stop existing server: %v\n", err)
				fmt.Println("Please manually stop the server and retry.")
				fmt.Println("To manually stop: kill the 'bun' process running on port " + port)
				return fmt.Errorf("failed to restart server: %w", err)
			}
			serverRunning = false
		} else {
			// Normal reuse of existing server
			fmt.Printf("Reusing OpenCode server at %s\n", url)
			a.serverURL = url
			_ = os.Setenv("OPENCODE_SERVER", url)
			return nil
		}
	}

	// Start new server (if not running or was stopped)
	if !serverRunning {
		if err := a.startServerProcess(defaultServerHost, port, url); err != nil {
			return err
		}

		if err := a.waitForServerReady(url, 15*time.Second); err != nil {
			a.stopServer()
			return fmt.Errorf("failed to start OpenCode server: %w", err)
		}
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

	// Add .opencode/node_modules to NODE_PATH so bun can resolve plugin dependencies
	opencodeNodeModules := filepath.Join(a.projectRoot, ".opencode", "node_modules")
	if info, err := os.Stat(opencodeNodeModules); err == nil && info.IsDir() {
		nodePath := os.Getenv("NODE_PATH")
		if nodePath != "" {
			nodePath = opencodeNodeModules + string(os.PathListSeparator) + nodePath
		} else {
			nodePath = opencodeNodeModules
		}
		cmd.Env = append(cmd.Env, "NODE_PATH="+nodePath)
	}

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
	// Use longer timeout for individual health checks to allow server initialization
	// Server needs time to load plugins, configs, and LSP servers on first request
	healthCheckTimeout := 5 * time.Second
	for time.Now().Before(deadline) {
		if a.serverReachable(url, healthCheckTimeout) {
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

// killExistingServer kills the bun process running on the specified port
func (a *App) killExistingServer(port string) error {
	// Use lsof to find the process listening on the port
	cmd := exec.Command("lsof", "-ti", fmt.Sprintf(":%s", port))
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to find process on port %s: %w", port, err)
	}

	pidStr := strings.TrimSpace(string(output))
	if pidStr == "" {
		return fmt.Errorf("no process found on port %s", port)
	}

	// Split by newlines in case there are multiple PIDs (parent and child processes)
	pids := strings.Split(pidStr, "\n")
	if len(pids) == 0 {
		return fmt.Errorf("no process found on port %s", port)
	}

	// Use the first PID (usually the main process)
	firstPidStr := strings.TrimSpace(pids[0])
	pid, err := strconv.Atoi(firstPidStr)
	if err != nil {
		return fmt.Errorf("invalid PID: %s", firstPidStr)
	}

	if len(pids) > 1 {
		fmt.Printf("Found %d processes on port %s, killing main process (PID: %d)\n", len(pids), port, pid)
	}

	// Find the process
	proc, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("failed to find process %d: %w", pid, err)
	}

	fmt.Printf("Stopping OpenCode server process (PID: %d)...\n", pid)

	// Try graceful shutdown with SIGTERM
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM to process %d: %w", pid, err)
	}

	// Wait up to 3 seconds for graceful shutdown
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		// Check if process is still running
		if err := proc.Signal(syscall.Signal(0)); err != nil {
			// Process is dead
			fmt.Println("OpenCode server stopped gracefully")
			return nil
		}
	}

	// Force kill if still running
	fmt.Println("Server didn't stop gracefully, forcing kill...")
	if err := proc.Kill(); err != nil {
		return fmt.Errorf("failed to kill process %d: %w", pid, err)
	}

	fmt.Println("OpenCode server killed")
	return nil
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

// waitForDaemonReady polls opencode-tmux status --json until daemon_running is true
func (a *App) waitForDaemonReady(sessionName string, timeoutSeconds int) error {
	deadline := time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
	for time.Now().Before(deadline) {
		cmd := exec.Command(a.binPath, "status", "--json", sessionName)
		cmd.Env = os.Environ()
		out, err := cmd.Output()
		if err == nil && len(out) > 0 {
			var status struct {
				DaemonRunning bool `json:"daemon_running"`
			}
			if jsonErr := json.Unmarshal(out, &status); jsonErr == nil {
				if status.DaemonRunning {
					return nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for daemon for '%s'", sessionName)
}

// listTmuxSessions returns available tmux session names
func listTmuxSessions() []string {
	if _, err := exec.LookPath("tmux"); err != nil {
		return nil
	}
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	res := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			res = append(res, l)
		}
	}
	return res
}

// promptChoice asks the user a yes/no/option question
func promptChoice(question string, options []string, def string) string {
	fmt.Printf("%s [%s] (default: %s): ", question, strings.Join(options, "/"), def)
	r := bufio.NewReader(os.Stdin)
	for {
		line, _ := r.ReadString('\n')
		v := strings.ToLower(strings.TrimSpace(line))
		if v == "" {
			return def
		}
		for _, o := range options {
			if v == strings.ToLower(o) {
				return v
			}
		}
		fmt.Printf("Please enter one of [%s]: ", strings.Join(options, "/"))
	}
}

// promptSelect lets the user select one item from a list
func promptSelect(question string, items []string, def string) string {
	fmt.Printf("%s\n", question)
	for i, it := range items {
		fmt.Printf("  %d) %s\n", i+1, it)
	}
	fmt.Printf("Enter number (default %s): ", def)
	r := bufio.NewReader(os.Stdin)
	for {
		line, _ := r.ReadString('\n')
		v := strings.TrimSpace(line)
		if v == "" {
			return def
		}
		idx, _ := strconv.Atoi(v)
		if idx >= 1 && idx <= len(items) {
			return items[idx-1]
		}
		fmt.Printf("Enter a number between 1 and %d: ", len(items))
	}
}

func containsFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

// attachToSession attaches to a tmux session
func (a *App) attachToSession(sessionName string, readOnly bool) error {
	// When already inside tmux, prefer switching the current client
	// instead of trying to attach (which errors with: "sessions should be nested with care").
	if os.Getenv("TMUX") != "" {
		// Best experience: switch the existing client to the target session.
		sc := exec.Command("tmux", "switch-client", "-t", sessionName)
		sc.Stdin = os.Stdin
		sc.Stdout = os.Stdout
		sc.Stderr = os.Stderr
		if err := sc.Run(); err == nil {
			return nil
		}

		// Fallback: force a nested attach by unsetting TMUX in the child env.
		// This mirrors tmux's own hint: "unset $TMUX to force".
		env := make([]string, 0, len(os.Environ()))
		for _, e := range os.Environ() {
			if !strings.HasPrefix(e, "TMUX=") {
				env = append(env, e)
			}
		}
		args := []string{"attach-session", "-t", sessionName}
		if readOnly {
			args = append(args, "-r")
		}
		cmd := exec.Command("tmux", args...)
		cmd.Env = env
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Not inside tmux: do a normal attach.
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

// isProjectRoot checks if directory is the TmuxCoder project root
func isProjectRoot(dir string) bool {
	// Check for project structure markers (more reliable than checking build artifacts)
	// Look for the cmd/tmuxcoder directory
	cmdDir := filepath.Join(dir, "cmd", "tmuxcoder")
	if info, err := os.Stat(cmdDir); err == nil && info.IsDir() {
		// Also verify opencode-tmux command exists
		opencodeCmd := filepath.Join(dir, "cmd", "opencode-tmux")
		if info2, err2 := os.Stat(opencodeCmd); err2 == nil && info2.IsDir() {
			return true
		}
	}
	return false
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

func resolveFilePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	if path == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = home
	} else if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}

	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		path = abs
	}
	return path, nil
}

func resolveLayoutPath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		if env := os.Getenv("OPENCODE_TMUX_CONFIG"); strings.TrimSpace(env) != "" {
			path = env
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("failed to determine layout path: %w", err)
			}
			path = filepath.Join(home, ".opencode", "tmux.yaml")
		}
	}
	return resolveFilePath(path)
}
