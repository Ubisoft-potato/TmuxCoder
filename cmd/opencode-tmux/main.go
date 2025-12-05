package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/opencode/tmux_coder/cmd/opencode-tmux/commands"
	"github.com/opencode/tmux_coder/internal/client"
	appconfig "github.com/opencode/tmux_coder/internal/config"
	tmuxconfig "github.com/opencode/tmux_coder/internal/config"
	"github.com/opencode/tmux_coder/internal/interfaces"
	"github.com/opencode/tmux_coder/internal/ipc"
	panelregistry "github.com/opencode/tmux_coder/internal/panel"
	"github.com/opencode/tmux_coder/internal/paths"
	"github.com/opencode/tmux_coder/internal/permission"
	"github.com/opencode/tmux_coder/internal/persistence"
	"github.com/opencode/tmux_coder/internal/session"
	"github.com/opencode/tmux_coder/internal/socket"
	"github.com/opencode/tmux_coder/internal/state"
	"github.com/opencode/tmux_coder/internal/supervision"
	"github.com/opencode/tmux_coder/internal/theme"
	"github.com/opencode/tmux_coder/internal/types"
	"github.com/sst/opencode-sdk-go"
	"github.com/sst/opencode-sdk-go/option"
)

// RunMode defines how the orchestrator handles signals
type RunMode int

const (
	// ModeForeground: Ctrl+C triggers cleanup shutdown (default, backward compatible)
	ModeForeground RunMode = iota

	// ModeDaemon: Ctrl+C ignored, only IPC shutdown commands stop the daemon
	ModeDaemon
)

// String returns the string representation of RunMode
func (m RunMode) String() string {
	switch m {
	case ModeForeground:
		return "Foreground"
	case ModeDaemon:
		return "Daemon"
	default:
		return "Unknown"
	}
}

// TmuxOrchestrator manages the tmux session and panels
type TmuxOrchestrator struct {
	sessionName      string
	socketPath       string
	statePath        string
	httpClient       *opencode.Client
	ipcServer        *ipc.SocketServer
	syncManager      *state.PanelSyncManager
	ctx              context.Context
	cancel           context.CancelFunc
	tmuxCommand      string
	isRunning        bool
	serverOnly       bool
	sseClient        *http.Client
	serverURL        string
	layout           *tmuxconfig.Layout
	panes            map[string]string
	reuseExisting    bool
	forceNewSession  bool
	attachOnly       bool
	configPath       string
	layoutMutex      sync.Mutex
	paneSupervisorMu sync.Mutex
	paneSupervisors  map[string]context.CancelFunc
	lock             *session.SessionLock
	messageRoles     map[string]string // Track message ID -> role mapping for handling parts
	messageRolesMu   sync.Mutex        // Protect messageRoles map

	// Stage 1: Infrastructure (not yet used, will be activated in later stages)
	clientTracker *client.ClientTracker

	// Stage 3: Signal Handling
	runMode RunMode // Default: ModeForeground for backward compatibility

	// Stage 4: Shutdown control
	cleanupOnExit bool // Whether to destroy tmux session on shutdown

	// Stage 5: Status tracking
	startedAt time.Time               // When the orchestrator was started
	owner     interfaces.SessionOwner // Session owner information

	// Stage 6: Process monitoring and health detection
	healthChecker *supervision.PaneHealthChecker
	appConfig     *appconfig.Config
}

// NewTmuxOrchestrator creates a new tmux orchestrator
func NewTmuxOrchestrator(sessionName, socketPath, statePath, serverURL string, httpClient *opencode.Client, serverOnly bool, layout *tmuxconfig.Layout, reuseExisting bool, forceNew bool, attachOnly bool, configPath string, runMode RunMode) *TmuxOrchestrator {
	ctx, cancel := context.WithCancel(context.Background())

	// Get current user information for session owner
	currentUser, err := ipc.GetCurrentUser()
	var owner interfaces.SessionOwner
	if err != nil {
		log.Printf("Warning: failed to get current user: %v", err)
		// Use fallback values
		owner = interfaces.SessionOwner{
			UID:       uint32(os.Getuid()),
			GID:       uint32(os.Getgid()),
			Username:  "unknown",
			Hostname:  "unknown",
			StartedAt: time.Now(),
		}
	} else {
		owner = ipc.ToSessionOwner(currentUser)
	}

	return &TmuxOrchestrator{
		sessionName:     sessionName,
		socketPath:      socketPath,
		statePath:       statePath,
		httpClient:      httpClient,
		ctx:             ctx,
		cancel:          cancel,
		tmuxCommand:     "tmux",
		serverOnly:      serverOnly,
		sseClient:       &http.Client{Timeout: 0}, // No timeout for SSE connections
		serverURL:       serverURL,
		layout:          layout,
		panes:           map[string]string{},
		paneSupervisors: map[string]context.CancelFunc{},
		messageRoles:    map[string]string{},
		reuseExisting:   reuseExisting,
		forceNewSession: forceNew,
		attachOnly:      attachOnly,
		configPath:      configPath,
		runMode:         runMode, // Stage 3: Signal handling mode
		startedAt:       time.Now(),
		owner:           owner,
	}
}

// Initialize sets up the orchestrator and its components
func (orch *TmuxOrchestrator) Initialize() error {
	log.Printf("Initializing tmux orchestrator...")

	// Create directories
	if err := orch.createDirectories(); err != nil {
		return fmt.Errorf("failed to create directories: %w", err)
	}

	// Initialize state management
	if err := orch.initializeStateManagement(); err != nil {
		return fmt.Errorf("failed to initialize state management: %w", err)
	}

	// Load existing sessions from OpenCode server
	if err := orch.loadSessionsFromServer(); err != nil {
		log.Printf("Warning: Failed to load sessions from server: %v", err)
		// Don't fail initialization if session loading fails - it's not critical
	}

	// Stage 2: Ensure socket is clean before starting IPC server
	if err := orch.ensureSocketClean(); err != nil {
		return fmt.Errorf("failed to prepare socket: %w", err)
	}

	// Start IPC server
	if err := orch.startIPCServer(); err != nil {
		return fmt.Errorf("failed to start IPC server: %w", err)
	}

	// Stage 1: Initialize client tracker (no-op, not monitoring yet)
	orch.clientTracker = client.NewClientTracker(
		orch.sessionName,
		orch.tmuxCommand,
		5*time.Second,
	)
	log.Printf("[Stage 1] Client tracker initialized (monitoring not started)")

	// Start API request handler and SSE client only if httpClient is available
	if orch.httpClient != nil {
		// Start API request handler for TUI control
		go orch.startAPIRequestHandler()

		// Start SSE client for real-time updates
		go orch.startSSEClient()
	} else {
		log.Printf("Server-only mode or HTTP client unavailable; skipping API handler and SSE client startup")
	}

	log.Printf("Tmux orchestrator initialized successfully")
	return nil
}

// Start creates and configures the tmux session with panels
func (orch *TmuxOrchestrator) Start() error {
	log.Printf("Starting tmux session: %s", orch.sessionName)

	// Stage 6: Load configuration
	if orch.configPath != "" {
		log.Printf("[Stage 6] Loading configuration from: %s", orch.configPath)
		cfg, err := appconfig.LoadConfig(orch.configPath)
		if err != nil {
			log.Printf("[Stage 6] Warning: failed to load config: %v, using defaults", err)
			orch.appConfig = appconfig.DefaultConfig()
		} else {
			if err := cfg.Validate(); err != nil {
				log.Printf("[Stage 6] Warning: invalid config: %v, using defaults", err)
				orch.appConfig = appconfig.DefaultConfig()
			} else {
				orch.appConfig = cfg
				log.Printf("[Stage 6] Configuration loaded successfully")
			}
		}
	} else {
		orch.appConfig = appconfig.DefaultConfig()
		log.Printf("[Stage 6] Using default configuration")
	}

	// Stage 6: Initialize health checker
	orch.healthChecker = supervision.NewPaneHealthChecker(
		orch.tmuxCommand,
		orch.sessionName,
	)
	log.Printf("[Stage 6] Health checker initialized")

	// In server-only mode, don't manage tmux session
	if orch.serverOnly {
		log.Printf("Server-only mode: skipping tmux session management")
		return nil
	}

	orch.panes = map[string]string{}

	// Check if tmux is available
	if !orch.isTmuxAvailable() {
		return fmt.Errorf("tmux is not available")
	}

	sessionExists := orch.sessionExists()

	needsConfiguration := false

	if sessionExists {
		if orch.forceNewSession {
			if err := orch.killTmuxSession(); err != nil {
				return fmt.Errorf("failed to stop existing session: %w", err)
			}
			sessionExists = false
		} else if orch.reuseExisting {
			if err := orch.resetTmuxWindow(); err != nil {
				return fmt.Errorf("failed to prepare existing session: %w", err)
			}
			needsConfiguration = true // Reuse requires reconfiguration
		} else {
			if err := orch.killTmuxSession(); err != nil {
				return fmt.Errorf("failed to stop existing session: %w", err)
			}
			sessionExists = false
		}
	}

	if !sessionExists {
		// Create tmux session
		if err := orch.createTmuxSession(); err != nil {
			return fmt.Errorf("failed to create tmux session: %w", err)
		}
		needsConfiguration = true // New session requires configuration
	}

	if orch.serverOnly {
		log.Printf("Server-only mode: skipping panel configuration and applications")
	} else if needsConfiguration {
		// Configure panels (for new sessions or reused sessions)
		if err := orch.configurePanels(); err != nil {
			return fmt.Errorf("failed to configure panels: %w", err)
		}

		// Stage 6: Recover existing session health if reusing
		if orch.reuseExisting && sessionExists {
			log.Printf("[Stage 6] Recovering existing session health...")
			if err := orch.recoverExistingSession(); err != nil {
				log.Printf("[Stage 6] Warning: session recovery encountered issues: %v", err)
				// Don't fail startup on recovery issues
			}
		}

		// Start panel applications
		if err := orch.startPanelApplications(); err != nil {
			return fmt.Errorf("failed to start panel applications: %w", err)
		}
	}

	orch.isRunning = true
	log.Printf("Tmux session started successfully")
	return nil
}

// Stop gracefully shuts down the tmux session and all components
func (orch *TmuxOrchestrator) Stop() error {
	log.Printf("Stopping tmux orchestrator...")

	orch.isRunning = false

	// Cancel context to signal shutdown
	orch.cancel()

	// ===== PHASE 1: Stop accepting new connections =====
	if orch.ipcServer != nil {
		log.Printf("[Shutdown] Stopping IPC server...")
		orch.ipcServer.Stop()
	}

	// ===== PHASE 2: Wait for existing IPC connections to close =====
	if orch.ipcServer != nil {
		log.Printf("[Shutdown] Waiting for IPC connections to close...")
		orch.waitForIPCConnectionsClose(5 * time.Second)
	}

	// ===== PHASE 3: Stop supervisors =====
	log.Printf("[Shutdown] Stopping panel supervisors...")
	orch.paneSupervisorMu.Lock()
	for paneTarget, cancel := range orch.paneSupervisors {
		log.Printf("[Shutdown] Stopping supervisor for pane %s", paneTarget)
		cancel()
	}
	orch.paneSupervisors = map[string]context.CancelFunc{}
	orch.paneSupervisorMu.Unlock()

	// ===== PHASE 4: Stop other components =====
	if orch.syncManager != nil {
		log.Printf("[Shutdown] Stopping sync manager...")
		orch.syncManager.Stop()
	}

	// ===== PHASE 5: Handle tmux session =====
	// Stage 4: Check cleanup flag
	if orch.cleanupOnExit {
		log.Printf("[Shutdown] Cleaning up tmux session (cleanup=true)...")
		if err := orch.killTmuxSession(); err != nil {
			log.Printf("[Shutdown] WARNING: Failed to kill tmux session: %v", err)
		}
	} else {
		log.Printf("[Shutdown] Preserving tmux session (cleanup=false)")
		log.Printf("[Shutdown] Session '%s' remains available", orch.sessionName)
		log.Printf("[Shutdown] WARNING: Panel processes are no longer supervised")
		log.Printf("[Shutdown] To restore management: opencode-tmux start %s --reuse", orch.sessionName)

		// Stage 6: Show warning in tmux status bar
		orch.setTmuxStatusBarWarning("⚠ Daemon stopped")
	}

	// ===== PHASE 6: Cleanup socket file (enhanced) =====
	log.Printf("[Shutdown] Cleaning up socket file...")
	if err := orch.cleanupSocket(); err != nil {
		log.Printf("[Shutdown] WARNING: Failed to cleanup socket: %v", err)
		// Don't return error - continue with shutdown
	}

	// ===== PHASE 7: Release lock (if exists) =====
	if orch.lock != nil {
		log.Printf("[Shutdown] Releasing session lock...")
		orch.lock.Release()
		orch.lock = nil
	}

	log.Printf("Tmux orchestrator stopped successfully")
	return nil
}

// ensureSocketClean ensures the socket file is in a usable state before starting the IPC server.
// It checks if the socket already exists and attempts to clean it up if it's stale.
//
// Returns:
//   - nil: Socket is clean and ready to use
//   - error: Socket conflict or permission issue
func (orch *TmuxOrchestrator) ensureSocketClean() error {
	status, err := socket.CheckSocketStatus(orch.socketPath)

	switch status {
	case socket.SocketNonExistent:
		// Ideal case - socket doesn't exist, can create directly
		log.Printf("[Socket] Path is clean: %s", orch.socketPath)
		return nil

	case socket.SocketStale:
		// Stale socket found - cleanup and continue
		log.Printf("[Socket] Found stale socket, cleaning up: %s", orch.socketPath)
		if err := socket.CleanupStaleSocket(orch.socketPath); err != nil {
			return fmt.Errorf("failed to cleanup stale socket: %w", err)
		}
		log.Printf("[Socket] Stale socket cleaned successfully")
		return nil

	case socket.SocketActive:
		// Another process is using this socket
		if orch.forceNewSession {
			// Force mode - warn but allow continuation
			log.Printf("[Socket] WARNING: Forcing cleanup of active socket: %s", orch.socketPath)
			log.Printf("[Socket] This may disconnect an existing orchestrator!")

			// Try to cleanup anyway
			if err := os.Remove(orch.socketPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to force remove active socket: %w", err)
			}
			return nil
		}

		// Normal mode - prevent starting
		return fmt.Errorf(
			"another orchestrator is already running with socket %s\n"+
				"  → To attach to existing session: tmuxcoder attach %s\n"+
				"  → To stop existing daemon:       tmuxcoder stop %s\n"+
				"  → To force new session:          tmuxcoder start %s --force-new",
			orch.socketPath, orch.sessionName, orch.sessionName, orch.sessionName,
		)

	case socket.SocketPermissionDenied:
		return fmt.Errorf(
			"permission denied to access socket %s: %w\n"+
				"  → Socket may be owned by another user\n"+
				"  → Check file permissions: ls -l %s",
			orch.socketPath, err, orch.socketPath,
		)

	default:
		if err != nil {
			return fmt.Errorf("unknown socket status error: %w", err)
		}
		return fmt.Errorf("unknown socket status: %v", status)
	}
}

// waitForIPCConnectionsClose waits for all IPC connections to close gracefully.
// This is called during shutdown to ensure all panel connections are properly closed
// before cleaning up the socket file.
//
// Parameters:
//   - timeout: Maximum time to wait for connections to close
//
// Behavior:
//   - Returns immediately if no connections
//   - Polls connection count every 100ms
//   - Logs warnings if connections remain after timeout
func (orch *TmuxOrchestrator) waitForIPCConnectionsClose(timeout time.Duration) {
	if orch.ipcServer == nil {
		return
	}

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		count := orch.ipcServer.ConnectionCount()
		if count == 0 {
			log.Printf("[IPC] All connections closed gracefully")
			return
		}

		select {
		case <-ticker.C:
			log.Printf("[IPC] Waiting for %d connection(s) to close...", count)
		case <-time.After(timeout):
			count := orch.ipcServer.ConnectionCount()
			log.Printf("[IPC] Timeout waiting for connections, %d still active", count)
			return
		}
	}

	count := orch.ipcServer.ConnectionCount()
	if count > 0 {
		log.Printf("[IPC] WARNING: %d connection(s) still active after timeout", count)
	}
}

// cleanupSocket performs safe socket file cleanup on shutdown.
// It checks the socket status before attempting removal and provides detailed logging.
//
// Returns:
//   - nil: Socket cleaned successfully or already removed
//   - error: Failed to remove socket (logged but not critical)
func (orch *TmuxOrchestrator) cleanupSocket() error {
	if orch.socketPath == "" {
		return nil
	}

	log.Printf("[Socket] Cleaning up socket: %s", orch.socketPath)

	// Check socket status before cleanup (for logging purposes)
	status, err := socket.CheckSocketStatus(orch.socketPath)
	if err != nil && status != socket.SocketStale && status != socket.SocketNonExistent {
		log.Printf("[Socket] WARNING: Failed to check socket status: %v", err)
		// Continue trying to delete anyway
	}

	// Log the status for debugging
	log.Printf("[Socket] Status before cleanup: %s", status.String())

	// Delete socket file
	if err := os.Remove(orch.socketPath); err != nil {
		if os.IsNotExist(err) {
			log.Printf("[Socket] Socket file already removed: %s", orch.socketPath)
			return nil
		}
		return fmt.Errorf("failed to remove socket file: %w", err)
	}

	log.Printf("[Socket] Socket file removed successfully: %s", orch.socketPath)
	return nil
}

// createDirectories creates necessary directories
func (orch *TmuxOrchestrator) createDirectories() error {
	dirs := []string{
		filepath.Dir(orch.socketPath),
		filepath.Dir(orch.statePath),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return nil
}

// initializeStateManagement sets up state management components
func (orch *TmuxOrchestrator) initializeStateManagement() error {
	// Create shared state
	sharedState := types.NewSharedApplicationState()

	// Create file manager
	fileManagerConfig := persistence.DefaultFileManagerConfig(orch.statePath)
	fileManager := persistence.NewFileManager(fileManagerConfig)

	// Create event bus
	eventBus := state.NewEventBus(1000)

	// Create conflict resolver
	conflictResolver := state.DefaultConflictResolver()

	// Create sync manager
	syncManagerConfig := state.DefaultSyncManagerConfig()
	orch.syncManager = state.NewPanelSyncManager(sharedState, fileManager, eventBus, conflictResolver, syncManagerConfig)

	// Create event channel for local state changes
	eventChan := make(chan types.StateEvent, 100)
	eventBus.Subscribe("tmux-orchestrator", "tmux-orchestrator", "orchestrator", eventChan)

	// Start goroutine to handle events
	go orch.handleEvents(eventChan)

	// Initialize sync manager
	if err := orch.syncManager.Initialize(); err != nil {
		return err
	}

	// Verify initialization
	if orch.syncManager == nil {
		return fmt.Errorf("sync manager is nil after initialization")
	}

	testState := orch.syncManager.GetState()
	if testState == nil {
		return fmt.Errorf("state manager returns nil state after initialization")
	}

	log.Printf("State management initialized successfully, initial version: %d", testState.Version.Version)
	log.Printf("State details - SessionID: %s, Theme: %s, UpdateCount: %d",
		testState.CurrentSessionID, testState.Theme, testState.UpdateCount)

	return nil
}

// handleLocalSessionChanged handles local session change events from panels
func (orch *TmuxOrchestrator) handleEvents(eventChan chan types.StateEvent) {
	for event := range eventChan {
		switch event.Type {
		case types.EventSessionChanged:
			if err := orch.handleLocalSessionChanged(event); err != nil {
				log.Printf("Error handling local session change: %v", err)
			}
		case types.EventThemeChanged:
			if err := orch.handleThemeChanged(event); err != nil {
				log.Printf("Error handling theme change: %v", err)
			}
		case types.EventPanelDisconnected:
			orch.handlePanelDisconnected(event)
		default:
			// Handle other event types if needed
			log.Printf("Received event: %s from panel %s", event.Type, event.SourcePanel)
		}
	}
}

func (orch *TmuxOrchestrator) handleLocalSessionChanged(event types.StateEvent) error {
	log.Printf("[TMUX] Handling local session change event: %+v", event)

	// Extract session ID from the event payload
	if payloadMap, ok := event.Data.(map[string]interface{}); ok {
		var payload types.SessionChangePayload
		if sessionIDRaw, exists := payloadMap["session_id"]; exists {
			if sessionID, ok := sessionIDRaw.(string); ok {
				payload.SessionID = sessionID
				log.Printf("[TMUX] Session changed to: %s", payload.SessionID)

				// The state has already been updated by the sync manager
				// We just need to log this for debugging purposes
				return nil
			}
		}
	}

	log.Printf("[TMUX] Failed to extract session ID from event payload")
	return nil
}

func (orch *TmuxOrchestrator) handleThemeChanged(event types.StateEvent) error {
	log.Printf("[TMUX] Handling theme change event: %+v", event)

	// Extract theme name from the event payload
	if payloadMap, ok := event.Data.(map[string]interface{}); ok {
		var payload types.ThemeChangePayload
		if themeRaw, exists := payloadMap["theme"]; exists {
			if theme, ok := themeRaw.(string); ok {
				payload.Theme = theme
				log.Printf("[TMUX] Theme changed to: %s", payload.Theme)

				// Apply the theme globally to all panels
				return orch.applyGlobalTheme(payload.Theme)
			}
		}
	}

	log.Printf("[TMUX] Failed to extract theme from event payload")
	return nil
}

func (orch *TmuxOrchestrator) handlePanelDisconnected(event types.StateEvent) {
	panelID, panelType := extractPanelConnectionInfo(event.Data)
	if panelID == "" && panelType == "" {
		log.Printf("[TMUX] panel disconnect event missing identifiers: %+v", event.Data)
		return
	}

	target := orch.getPaneTarget(panelID, panelType)
	if strings.TrimSpace(target) == "" {
		log.Printf("[TMUX] No pane target recorded for panel %s (%s)", panelID, panelType)
		return
	}

	appName, err := orch.getPanelAppName(panelID, panelType)
	if err != nil {
		log.Printf("[TMUX] Unable to resolve app for panel %s (%s): %v", panelID, panelType, err)
		return
	}

	for attempt := 0; attempt < 3; attempt++ {
		if orch.paneExists(target) && orch.paneMatchesApp(target, appName) {
			break
		}
		log.Printf("[TMUX] Pane %s for panel %s (%s) missing/stale (attempt %d); attempting recover", target, panelID, panelType, attempt+1)
		recovered, err := orch.recoverMissingPane(panelID, panelType)
		if err != nil {
			log.Printf("[TMUX] Failed to recover pane for %s (%s): %v", panelID, panelType, err)
			return
		}
		if strings.TrimSpace(recovered) == "" {
			log.Printf("[TMUX] Pane recovery did not yield target for %s (%s)", panelID, panelType)
			return
		}
		if !orch.paneExists(recovered) {
			log.Printf("[TMUX] Pane %s still unavailable after recovery", recovered)
			return
		}
		log.Printf("[TMUX] Pane %s recovered and ready", recovered)
		target = recovered
	}
	orch.updatePaneTarget(panelID, panelType, target)
	if !orch.paneExists(target) {
		log.Printf("[TMUX] Pane %s for panel %s (%s) unavailable after recovery attempts; performing full window recovery", target, panelID, panelType)
		if err := orch.fullWindowRecovery(); err != nil {
			log.Printf("[TMUX] Full window recovery failed: %v", err)
			return
		}
		target = orch.getPaneTarget(panelID, panelType)
		orch.updatePaneTarget(panelID, panelType, target)
		if !orch.paneExists(target) {
			log.Printf("[TMUX] Pane %s for panel %s (%s) still unavailable after full recovery", target, panelID, panelType)
			return
		}
	}

	if orch.paneExists(target) && orch.paneMatchesApp(target, appName) {
		log.Printf("[TMUX] Pane %s for panel %s (%s) already running %s; skipping restart", target, panelID, panelType, appName)
		return
	}

	envVars := map[string]string{
		"OPENCODE_SERVER": os.Getenv("OPENCODE_SERVER"),
		"OPENCODE_SOCKET": orch.socketPath,
	}

	go func(panelID, panelType, paneTarget, app string) {
		log.Printf("[TMUX] Restarting panel %s (%s) after disconnect", panelID, panelType)
		if err := orch.startPanelApp(paneTarget, app, envVars); err != nil {
			log.Printf("[TMUX] Failed to restart panel %s (%s): %v", panelID, panelType, err)
		}
	}(panelID, panelType, target, appName)
}

// applyGlobalTheme applies the theme to all connected panels
func (orch *TmuxOrchestrator) applyGlobalTheme(themeName string) error {
	log.Printf("[TMUX] Applying global theme: %s", themeName)

	// Actually set the theme in the theme manager
	if err := theme.SetTheme(themeName); err != nil {
		log.Printf("[TMUX] Error setting theme: %v", err)
		return err
	}

	// The theme has been updated in the theme manager
	// All connected panels will receive the theme change through the state sync mechanism

	if orch.ipcServer != nil {
		connections := orch.ipcServer.GetConnections()
		log.Printf("[TMUX] Theme applied to %d connected panels", len(connections))
		for _, conn := range connections {
			log.Printf("[TMUX] - Panel %s (%s) received theme update", conn.PanelID, conn.PanelType)
		}
	}

	return nil
}

// startIPCServer starts the IPC server for panel communication
func (orch *TmuxOrchestrator) startIPCServer() error {
	// Create IPC server
	orch.ipcServer = ipc.NewSocketServer(
		orch.socketPath,
		orch.syncManager.GetEventBus(),
		orch.syncManager,
		orch,
	)

	// Stage 5: Set up permission checker with default policy
	permissionChecker := permission.NewChecker(orch.owner, nil)
	orch.ipcServer.SetPermissionChecker(permissionChecker)
	log.Printf("Permission checker configured for session owner: %s (UID=%d, GID=%d)",
		orch.owner.Username, orch.owner.UID, orch.owner.GID)

	// Start server
	if err := orch.ipcServer.Start(); err != nil {
		return err
	}

	return nil
}

// isTmuxAvailable checks if tmux is available on the system
func (orch *TmuxOrchestrator) isTmuxAvailable() bool {
	_, err := exec.LookPath(orch.tmuxCommand)
	return err == nil
}

// createTmuxSession creates a new tmux session
func (orch *TmuxOrchestrator) createTmuxSession() error {
	// Create new session
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "new-session", "-d", "-s", orch.sessionName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}

	return nil
}

// sessionExists checks if the tmux session already exists.
func (orch *TmuxOrchestrator) sessionExists() bool {
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "has-session", "-t", orch.sessionName)
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

// resetTmuxWindow replaces the primary window with a fresh one for reconfiguration.
func (orch *TmuxOrchestrator) resetTmuxWindow() error {
	target := fmt.Sprintf("%s:0", orch.sessionName)

	// Capture the current window id so we can remove it before creating a replacement.
	currentWindowID, err := orch.getWindowID(target)
	if err != nil {
		log.Printf("Warning: failed to resolve current window id for %s: %v", target, err)
		currentWindowID = ""
	}

	// Create a replacement window and capture its id.
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "new-window", "-d", "-t", orch.sessionName, "-P", "-F", "#{window_id}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create replacement window: %w", err)
	}
	newWindowID := strings.TrimSpace(string(output))
	if newWindowID == "" {
		return fmt.Errorf("tmux did not return a window id for the replacement window")
	}

	// Remove the previous window BEFORE moving, to avoid index conflict.
	// This must happen before move-window because tmux can't move to an occupied index.
	if currentWindowID != "" {
		killCmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "kill-window", "-t", currentWindowID)
		if err := killCmd.Run(); err != nil {
			log.Printf("Warning: failed to remove previous window %s: %v", currentWindowID, err)
		}
	}

	// Move the replacement to index 0 so the rest of the code can target session:0 as usual.
	moveCmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "move-window", "-s", newWindowID, "-t", target)
	if err := moveCmd.Run(); err != nil {
		return fmt.Errorf("failed to move replacement window to %s: %w", target, err)
	}

	// Ensure attached clients land on the refreshed window.
	selectCmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "select-window", "-t", target)
	if err := selectCmd.Run(); err != nil {
		log.Printf("Warning: failed to select refreshed window %s: %v", target, err)
	}

	return nil
}

func (orch *TmuxOrchestrator) resetExistingSession() error {
	log.Printf("Resetting tmux session for fresh start: %s", orch.sessionName)

	if err := orch.killTmuxSession(); err != nil {
		return err
	}

	if err := os.Remove(orch.statePath); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove state file %s: %v", orch.statePath, err)
	} else if err == nil {
		log.Printf("State file cleared for fresh start: %s", orch.statePath)
	}

	if err := os.Remove(orch.socketPath); err != nil && !os.IsNotExist(err) {
		log.Printf("Warning: failed to remove socket file %s: %v", orch.socketPath, err)
	} else if err == nil {
		log.Printf("Socket file cleared: %s", orch.socketPath)
	}

	orch.reuseExisting = false
	return nil
}

// configurePanels configures the tmux panel layout
func (orch *TmuxOrchestrator) configurePanels() error {
	if orch.layout != nil {
		return orch.configurePanelsFromConfig()
	}
	return orch.configureDefaultPanels()
}

func (orch *TmuxOrchestrator) configureDefaultPanels() error {
	sessionTarget := orch.sessionName + ":0"

	// Split window horizontally (sessions + messages)
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "split-window", "-h", "-t", sessionTarget)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to split window horizontally: %w", err)
	}

	// Split the bottom pane for input (full width)
	cmd = exec.CommandContext(orch.ctx, orch.tmuxCommand, "split-window", "-v", "-t", sessionTarget+".1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to split window vertically: %w", err)
	}

	// Adjust pane sizes
	// Sessions panel: 20% width
	if err := orch.resizePane(sessionTarget+".0", "x", "20%"); err != nil {
		log.Printf("Warning: failed to resize sessions pane: %v", err)
	}

	// Input panel: 20% height
	if err := orch.resizePane(sessionTarget+".2", "y", "20%"); err != nil {
		log.Printf("Warning: failed to resize input pane: %v", err)
	}

	panes := map[string]string{
		"sessions": sessionTarget + ".0",
		"messages": sessionTarget + ".1",
		"input":    sessionTarget + ".2",
	}
	panes = orch.normalizePaneMap(panes)
	orch.panes = panes
	orch.logPaneAssignments("configure_default", orch.panes)

	return nil
}

func (orch *TmuxOrchestrator) prepareExistingSession() error {
	log.Printf("[prepareExistingSession] serverOnly=%v, reuseExisting=%v, forceNewSession=%v",
		orch.serverOnly, orch.reuseExisting, orch.forceNewSession)

	if orch.serverOnly {
		log.Printf("[prepareExistingSession] Server-only mode, skipping session preparation")
		return nil
	}

	sessionExists := orch.sessionExists()

	if orch.attachOnly {
		if !sessionExists {
			return fmt.Errorf("cannot attach: tmux session %s does not exist", orch.sessionName)
		}
		if err := orch.ensureDaemonRunningForAttach(); err != nil {
			return err
		}
		return nil
	}

	if !sessionExists {
		return nil
	}

	if orch.forceNewSession {
		return orch.resetExistingSession()
	}

	if orch.reuseExisting {
		log.Printf("Existing tmux session detected; reusing without prompt")
		return nil
	}

	if !isTerminal() {
		log.Printf("Existing tmux session detected but stdin is not a terminal; defaulting to reuse")
		orch.reuseExisting = true
		return nil
	}

	fmt.Printf("An existing tmux session has been detected: %s\n", orch.sessionName)
	fmt.Printf("Choose an action: [r] Reuse (default) / [n] Create new / [a] Attach only / [q] Exit: ")

	reader := bufio.NewReader(os.Stdin)

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("failed to read user input: %w", err)
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "" {
			choice = "r"
		}

		switch choice {
		case "r", "reuse", "y":
			orch.reuseExisting = true
			return nil
		case "n", "new":
			if err := orch.resetExistingSession(); err != nil {
				return err
			}
			return nil
		case "a", "attach":
			if err := orch.ensureDaemonRunningForAttach(); err != nil {
				fmt.Printf("%s\n", err.Error())
				fmt.Printf("Choose another action: [r] Reuse / [n] Create new / [q] Exit: ")
				continue
			}
			orch.attachOnly = true
			return nil
		case "q", "quit":
			return fmt.Errorf("user cancels startup")
		default:
			fmt.Printf("Invalid input, please enter r / n / a / q: ")
		}
	}
}

func (orch *TmuxOrchestrator) ensureDaemonRunningForAttach() error {
	status, err := socket.CheckSocketStatus(orch.socketPath)
	if err != nil && status != socket.SocketNonExistent && status != socket.SocketStale {
		return fmt.Errorf("failed to check orchestrator socket: %w", err)
	}

	if status != socket.SocketActive {
		return fmt.Errorf("cannot attach-only: orchestrator daemon is not running (socket: %s)\nUse 'opencode-tmux start %s --reuse' to restart it.", orch.socketPath, orch.sessionName)
	}
	return nil
}

func (orch *TmuxOrchestrator) configurePanelsFromConfig() error {
	sessionTarget := orch.sessionName + ":0"
	rootPane, err := orch.resolvePaneID(sessionTarget)
	if err != nil {
		return fmt.Errorf("failed to resolve root pane id: %w", err)
	}
	panes, err := orch.buildLayout(rootPane, orch.layout)
	if err != nil {
		return err
	}
	orch.panes = panes
	orch.logPaneAssignments("configure_panels", orch.panes)
	return nil
}

// buildLayout applies the configured layout starting from the provided root pane.
func (orch *TmuxOrchestrator) buildLayout(rootPane string, layout *tmuxconfig.Layout) (map[string]string, error) {
	if layout == nil {
		return nil, fmt.Errorf("layout configuration is not available")
	}

	panes := map[string]string{
		"root": rootPane,
	}

	type paneLock struct {
		lockX bool
		lockY bool
	}
	sizeLocks := map[string]*paneLock{}

	getLock := func(id string) *paneLock {
		lock, ok := sizeLocks[id]
		if ok {
			return lock
		}
		lock = &paneLock{}
		sizeLocks[id] = lock
		return lock
	}

	for _, split := range layout.Splits {
		targetPane, ok := panes[split.Target]
		if !ok {
			return nil, fmt.Errorf("unknown split target: %s", split.Target)
		}

		if len(split.Panels) != 2 {
			return nil, fmt.Errorf("split %s must define exactly two panels", split.Target)
		}

		args := []string{"split-window", "-P", "-F", "#{pane_id}"}
		typ := strings.ToLower(strings.TrimSpace(split.Type))
		if typ == "horizontal" {
			args = append(args, "-h")
		} else {
			args = append(args, "-v")
		}

		if split.Ratio != "" {
			_, secondPct, ok := layout.RatioPercents(split.Ratio)
			if ok {
				args = append(args, "-p", fmt.Sprintf("%d", secondPct))
				lockA := getLock(split.Panels[0])
				lockB := getLock(split.Panels[1])
				if typ == "horizontal" {
					lockA.lockX = true
					lockB.lockX = true
				} else {
					lockA.lockY = true
					lockB.lockY = true
				}
			} else {
				log.Printf("Invalid ratio %q for split %s, falling back to tmux defaults", split.Ratio, split.Target)
			}
		}

		args = append(args, "-t", targetPane)

		cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, args...)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("failed to split pane %s (%s): %w", split.Target, split.Type, err)
		}
		newPane := strings.TrimSpace(string(out))
		if newPane == "" {
			return nil, fmt.Errorf("tmux did not return a pane id for split target %s", split.Target)
		}

		first := split.Panels[0]
		second := split.Panels[1]

		// The original pane becomes the first panel; the new pane is the second.
		panes[first] = targetPane
		panes[second] = newPane
	}

	for _, panel := range layout.Panels {
		targetPane, ok := panes[panel.ID]
		if !ok {
			continue
		}
		lock := sizeLocks[panel.ID]

		width := strings.TrimSpace(panel.Width)
		if width != "" && (lock == nil || !lock.lockX) {
			if err := orch.resizePane(targetPane, "x", width); err != nil {
				log.Printf("Failed to apply width for %s: %v", panel.ID, err)
			}
		}

		height := strings.TrimSpace(panel.Height)
		if height != "" && (lock == nil || !lock.lockY) {
			if err := orch.resizePane(targetPane, "y", height); err != nil {
				log.Printf("Failed to apply height for %s: %v", panel.ID, err)
			}
		}
	}

	return orch.normalizePaneMap(panes), nil
}

func (orch *TmuxOrchestrator) resolvePaneID(target string) (string, error) {
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", target, "#{pane_id}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (orch *TmuxOrchestrator) getWindowID(target string) (string, error) {
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", target, "#{window_id}")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (orch *TmuxOrchestrator) handleSessionCompactedEvent(sessionID string) error {
	log.Printf("[SSE] Session compacted: %s", sessionID)

	data := make(map[string]interface{})
	if sessionID != "" {
		data["session_id"] = sessionID
	}

	if err := orch.triggerUIAction("refresh_messages", data); err != nil {
		return fmt.Errorf("failed to trigger messages refresh: %w", err)
	}

	return nil
}

func (orch *TmuxOrchestrator) triggerUIAction(action string, data map[string]interface{}) error {
	update := types.StateUpdate{
		ID:              fmt.Sprintf("ui_action_%s_%d", action, time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: action,
			Data:   data,
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	return orch.syncManager.UpdateWithVersionCheck(update)
}

// startPanelApplications starts the applications in each panel
func (orch *TmuxOrchestrator) startPanelApplications() error {
	if orch.layout != nil {
		return orch.startConfigPanelApplications()
	}
	return orch.startDefaultPanelApplications()
}

func (orch *TmuxOrchestrator) startDefaultPanelApplications() error {
	sessionTarget := orch.sessionName + ":0"

	// Set environment variables for all panels
	envVars := map[string]string{
		"OPENCODE_SERVER": os.Getenv("OPENCODE_SERVER"),
		"OPENCODE_SOCKET": orch.socketPath,
	}

	log.Printf("Starting panel applications with IPC socket: %s", orch.socketPath)

	// Wait a moment for IPC server to be fully ready
	time.Sleep(1 * time.Second)

	// Define panel configurations
	panels := []struct {
		pane string
		name string
		desc string
	}{
		{sessionTarget + ".0", "opencode-sessions", "sessions panel"},
		{sessionTarget + ".1", "opencode-messages", "messages panel"},
		{sessionTarget + ".2", "opencode-input", "input panel"},
	}

	// Start each panel with error recovery
	for i, panel := range panels {
		log.Printf("Starting %s (%d/3)...", panel.desc, i+1)

		if err := orch.startPanelApp(panel.pane, panel.name, envVars); err != nil {
			log.Printf("Failed to start %s: %v", panel.desc, err)
			// Don't fail completely - continue with other panels
			continue
		}

		// Give each panel time to start before starting the next
		time.Sleep(500 * time.Millisecond)
		log.Printf("✅ %s started successfully", panel.desc)
	}

	// Verify at least one panel is running
	if orch.verifyPanelsRunning() {
		log.Printf("Panel startup completed - at least one panel is running")
		return nil
	} else {
		return fmt.Errorf("no panels could be started successfully")
	}
}

func (orch *TmuxOrchestrator) startConfigPanelApplications() error {
	envVars := map[string]string{
		"OPENCODE_SERVER": os.Getenv("OPENCODE_SERVER"),
		"OPENCODE_SOCKET": orch.socketPath,
	}

	log.Printf("Starting panel applications with IPC socket: %s", orch.socketPath)
	time.Sleep(1 * time.Second)

	success := 0
	for idx, panel := range orch.layout.Panels {
		target, ok := orch.panes[panel.ID]
		if !ok {
			log.Printf("Pane target for %s not found; skipping", panel.ID)
			continue
		}

		appName, err := resolvePanelAppName(panel)
		if err != nil {
			log.Printf("Failed to resolve panel %s: %v", panel.ID, err)
			continue
		}

		log.Printf("Starting %s panel (%d/%d)...", panel.ID, idx+1, len(orch.layout.Panels))
		if err := orch.startPanelApp(target, appName, envVars); err != nil {
			log.Printf("Failed to start %s panel: %v", panel.ID, err)
			continue
		}

		time.Sleep(500 * time.Millisecond)
		log.Printf("✅ %s panel started successfully", panel.ID)
		success++
	}

	if success == 0 {
		return fmt.Errorf("no panels could be started successfully")
	}

	if orch.verifyPanelsRunning() {
		log.Printf("Panel startup completed - %d panels requested", success)
		return nil
	}

	return fmt.Errorf("panels failed health check after startup")
}

func resolvePanelAppName(panelCfg tmuxconfig.Panel) (string, error) {
	custom := strings.TrimSpace(panelCfg.Command)
	if custom != "" {
		return custom, nil
	}

	if module := strings.TrimSpace(panelCfg.Module); module != "" {
		inst, meta, err := panelregistry.Resolve(module)
		if err != nil {
			return "", fmt.Errorf("resolve panel module %s: %w", module, err)
		}
		if len(meta.DefaultCommand) == 0 {
			return "", fmt.Errorf("panel module %s has no default command", module)
		}
		_ = inst
		return meta.DefaultCommand[0], nil
	}

	key := strings.ToLower(strings.TrimSpace(panelCfg.Type))
	if key != "" {
		if _, meta, err := panelregistry.Resolve(key); err == nil {
			if len(meta.DefaultCommand) > 0 {
				return meta.DefaultCommand[0], nil
			}
		}
	}
	switch key {
	case "sessions":
		return "opencode-sessions", nil
	case "messages":
		return "opencode-messages", nil
	case "input":
		return "opencode-input", nil
	case "shell":
		// For shell type, return the user's default shell or bash
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/bash"
		}
		return shell, nil
	}

	return "", fmt.Errorf("unsupported panel type: %s", panelCfg.Type)
}

// startPanelApp starts an application in a specific tmux pane
func (orch *TmuxOrchestrator) startPanelApp(paneTarget, appName string, envVars map[string]string) error {
	normalizedTarget := orch.normalizePaneTarget(paneTarget)
	if err := orch.launchPaneProcess(normalizedTarget, appName, envVars); err != nil {
		return err
	}

	orch.startPaneSupervisor(normalizedTarget, appName, envVars)
	return nil
}

func (orch *TmuxOrchestrator) launchPaneProcess(paneTarget, appName string, envVars map[string]string) error {
	paneTarget = orch.normalizePaneTarget(paneTarget)

	run := strings.TrimSpace(appName)
	binaryPath, err := orch.getBinaryPath(appName)
	if err == nil {
		run = binaryPath
	} else {
		log.Printf("[DEBUG] Using raw command for %s: %v", appName, err)
	}
	if run == "" {
		return fmt.Errorf("no command for panel %s", appName)
	}

	command := orch.buildPaneCommand(run, envVars)
	log.Printf("[DEBUG] Respawning pane %s with command: %s", paneTarget, command)

	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "respawn-pane", "-k", "-t", paneTarget, command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput != "" {
			log.Printf("[ERROR] tmux respawn-pane output for %s: %s", paneTarget, trimmedOutput)
		}
		return fmt.Errorf("failed to respawn pane %s: %w", paneTarget, err)
	}

	if !orch.waitForPaneProcess(paneTarget, 3*time.Second) {
		return fmt.Errorf("pane %s did not become active after launching %s", paneTarget, appName)
	}

	return nil
}

func (orch *TmuxOrchestrator) normalizePaneTarget(paneTarget string) string {
	target := strings.TrimSpace(paneTarget)
	if target == "" {
		return target
	}
	// If the target is already in session:window.pane form, keep it.
	if strings.Contains(target, ":") && strings.Contains(target, ".") && !strings.HasPrefix(target, "%") && isQualifiedPaneTarget(target) {
		return target
	}
	if strings.HasPrefix(target, "%") {
		cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", target, "#{session_name}:#{window_index}.#{pane_index}")
		output, err := cmd.CombinedOutput()
		if err != nil {
			log.Printf("[DEBUG] Failed to normalize pane target %s: %v", target, err)
			return target
		}
		normalized := strings.TrimSpace(string(output))
		if normalized != "" && isQualifiedPaneTarget(normalized) {
			return normalized
		}
		log.Printf("[DEBUG] Normalized pane target %s produced ambiguous value %q; keeping original", target, normalized)
	}
	return target
}

func (orch *TmuxOrchestrator) normalizePaneMap(panes map[string]string) map[string]string {
	normalized := make(map[string]string, len(panes))
	for id, target := range panes {
		normalized[id] = orch.normalizePaneTarget(target)
	}
	return normalized
}

func (orch *TmuxOrchestrator) logPaneAssignments(context string, panes map[string]string) {
	if len(panes) == 0 {
		log.Printf("[DEBUG] pane assignment (%s): <none>", context)
		return
	}
	for id, target := range panes {
		normalized := orch.normalizePaneTarget(target)
		if normalized != target {
			log.Printf("[DEBUG] pane assignment (%s): %s -> %s (%s)", context, id, target, normalized)
		} else {
			log.Printf("[DEBUG] pane assignment (%s): %s -> %s", context, id, target)
		}
	}
}

func isQualifiedPaneTarget(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 2 {
		return false
	}
	if strings.TrimSpace(parts[0]) == "" {
		return false
	}
	sub := strings.Split(parts[1], ".")
	if len(sub) != 2 {
		return false
	}
	if _, err := strconv.Atoi(sub[0]); err != nil {
		return false
	}
	if _, err := strconv.Atoi(sub[1]); err != nil {
		return false
	}
	return true
}

func (orch *TmuxOrchestrator) paneExists(target string) bool {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return false
	}
	checkTargets := []string{}
	normalized := orch.normalizePaneTarget(trimmed)
	if normalized != trimmed {
		checkTargets = append(checkTargets, normalized)
	}
	checkTargets = append(checkTargets, trimmed)
	for _, t := range checkTargets {
		if strings.TrimSpace(t) == "" {
			continue
		}
		cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", t, "#{pane_id}")
		if err := cmd.Run(); err == nil {
			return true
		}
	}
	return false
}

func (orch *TmuxOrchestrator) recoverMissingPane(panelID, panelType string) (string, error) {
	log.Printf("[TMUX] Attempting pane recovery for %s (%s)", panelID, panelType)
	if orch.layout != nil {
		if err := orch.ReloadLayout(); err != nil {
			return "", err
		}
	} else {
		if err := orch.resetTmuxWindow(); err != nil {
			return "", err
		}
		if err := orch.configureDefaultPanels(); err != nil {
			return "", err
		}
	}
	return orch.getPaneTarget(panelID, panelType), nil
}

func (orch *TmuxOrchestrator) paneMatchesApp(paneTarget, appName string) bool {
	target := orch.normalizePaneTarget(paneTarget)
	if strings.TrimSpace(target) == "" {
		return false
	}
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", target, "#{pane_current_command}")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	current := filepath.Base(strings.TrimSpace(string(output)))
	if current == "" {
		return false
	}
	for _, expected := range orch.expectedCommandNames(appName) {
		if current == expected {
			return true
		}
	}
	return false
}

func (orch *TmuxOrchestrator) expectedCommandNames(appName string) []string {
	names := []string{}
	if base := filepath.Base(appName); base != "" {
		names = append(names, base)
	}
	if binary, err := orch.getBinaryPath(appName); err == nil {
		if b := filepath.Base(binary); b != "" {
			names = append(names, b)
		}
	}
	return uniqueStrings(names)
}

func (orch *TmuxOrchestrator) fullWindowRecovery() error {
	if err := orch.resetTmuxWindow(); err != nil {
		return err
	}

	if orch.layout != nil {
		if err := orch.configurePanelsFromConfig(); err != nil {
			return err
		}
		return orch.startConfigPanelApplications()
	}

	if err := orch.configureDefaultPanels(); err != nil {
		return err
	}
	return orch.startDefaultPanelApplications()
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		result = append(result, v)
	}
	return result
}

func extractPanelConnectionInfo(data interface{}) (string, string) {
	if payload, ok := data.(types.PanelConnectionPayload); ok {
		return payload.PanelID, payload.PanelType
	}
	if payloadMap, ok := data.(map[string]interface{}); ok {
		var panelID, panelType string
		if v, exists := payloadMap["panel_id"]; exists {
			if s, ok := v.(string); ok {
				panelID = s
			}
		}
		if v, exists := payloadMap["panel_type"]; exists {
			if s, ok := v.(string); ok {
				panelType = s
			}
		}
		return panelID, panelType
	}
	return "", ""
}

func (orch *TmuxOrchestrator) getPaneTarget(panelID, panelType string) string {
	orch.layoutMutex.Lock()
	defer orch.layoutMutex.Unlock()

	if panelID != "" {
		if target, ok := orch.panes[panelID]; ok {
			return target
		}
	}
	if panelType != "" {
		if target, ok := orch.panes[panelType]; ok {
			return target
		}
	}

	sessionTarget := orch.sessionName + ":0"
	switch strings.ToLower(panelType) {
	case "sessions":
		return sessionTarget + ".0"
	case "messages":
		return sessionTarget + ".1"
	case "input":
		return sessionTarget + ".2"
	}
	if panelID != "" {
		if target, ok := orch.panes[panelID]; ok {
			return target
		}
	}
	return ""
}

func (orch *TmuxOrchestrator) updatePaneTarget(panelID, panelType, target string) {
	target = orch.normalizePaneTarget(target)
	if strings.TrimSpace(target) == "" {
		return
	}
	orch.layoutMutex.Lock()
	logged := map[string]string{}
	if panelID != "" {
		orch.panes[panelID] = target
		logged[panelID] = target
	}
	if panelType != "" {
		orch.panes[panelType] = target
		logged[panelType] = target
	}
	orch.layoutMutex.Unlock()
	if len(logged) > 0 {
		orch.logPaneAssignments("update", logged)
	}
}

func (orch *TmuxOrchestrator) getPanelAppName(panelID, panelType string) (string, error) {
	if orch.layout != nil {
		for _, panel := range orch.layout.Panels {
			if panel.ID == panelID || (panelID == "" && strings.EqualFold(panel.Type, panelType)) {
				return resolvePanelAppName(panel)
			}
		}
	}

	switch strings.ToLower(panelType) {
	case "sessions", "opencode-sessions":
		return "opencode-sessions", nil
	case "messages", "opencode-messages":
		return "opencode-messages", nil
	case "input", "opencode-input":
		return "opencode-input", nil
	}

	switch panelID {
	case "sessions":
		return "opencode-sessions", nil
	case "messages":
		return "opencode-messages", nil
	case "input":
		return "opencode-input", nil
	}

	return "", fmt.Errorf("unknown app for panel %s (%s)", panelID, panelType)
}

func (orch *TmuxOrchestrator) startPaneSupervisor(paneTarget, appName string, envVars map[string]string) {
	// Copy env vars to avoid later mutation
	envCopy := cloneStringMap(envVars)
	orch.paneSupervisorMu.Lock()
	if cancel, exists := orch.paneSupervisors[paneTarget]; exists {
		cancel()
	}
	supervisorCtx, cancel := context.WithCancel(orch.ctx)
	orch.paneSupervisors[paneTarget] = cancel
	orch.paneSupervisorMu.Unlock()

	go orch.monitorPane(supervisorCtx, paneTarget, appName, envCopy)
}

func (orch *TmuxOrchestrator) monitorPane(ctx context.Context, paneTarget, appName string, envVars map[string]string) {
	// Stage 6: Use configured health check interval and delays
	checkInterval := 2 * time.Second
	initialDelay := time.Second
	maxDelay := 30 * time.Second

	if orch.appConfig != nil && orch.appConfig.Supervision.Enabled {
		checkInterval = orch.appConfig.Supervision.HealthCheckInterval
		initialDelay = orch.appConfig.Supervision.RestartDelay
		maxDelay = orch.appConfig.Supervision.MaxRestartDelay
		log.Printf("[Monitor] Using configured intervals: check=%v, restart=%v, max=%v",
			checkInterval, initialDelay, maxDelay)
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	retryDelay := initialDelay

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			health := orch.healthChecker.CheckPaneHealth(paneTarget)

			if health == supervision.PaneHealthy {
				retryDelay = initialDelay
				continue
			}

			log.Printf("[WARN] Pane %s for %s unhealthy (status: %s); attempting restart", paneTarget, appName, health)
			if err := orch.launchPaneProcess(paneTarget, appName, envVars); err != nil {
				log.Printf("[ERROR] Failed to restart pane %s: %v", paneTarget, err)
				time.Sleep(retryDelay)
				retryDelay *= 2
				if retryDelay > maxDelay {
					retryDelay = maxDelay
				}
				continue
			}
			retryDelay = initialDelay
		}
	}
}

func (orch *TmuxOrchestrator) buildPaneCommand(run string, envVars map[string]string) string {
	assignments := make([]string, 0, len(envVars))
	keys := make([]string, 0, len(envVars))
	for key := range envVars {
		if envVars[key] == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := envVars[key]
		assignments = append(assignments, fmt.Sprintf("%s=%s", key, shellEscape(value)))
	}

	inner := shellEscape("exec " + run)
	if len(assignments) > 0 {
		return fmt.Sprintf("env %s sh -lc %s", strings.Join(assignments, " "), inner)
	}
	return fmt.Sprintf("sh -lc %s", inner)
}

func (orch *TmuxOrchestrator) waitForPaneProcess(paneTarget string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := orch.isPaneAlive(paneTarget)
		if err == nil && alive {
			return true
		}
		if err != nil {
			log.Printf("[DEBUG] Checking pane %s status failed: %v", paneTarget, err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}

func (orch *TmuxOrchestrator) isPaneAlive(paneTarget string) (bool, error) {
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "display-message", "-p", "-t", paneTarget, "#{pane_dead}")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(output)) == "0", nil
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return map[string]string{}
	}
	copy := make(map[string]string, len(src))
	for key, value := range src {
		copy[key] = value
	}
	return copy
}

func shellEscape(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// getBinaryPath returns the correct binary path for a panel application
func (orch *TmuxOrchestrator) getBinaryPath(appName string) (string, error) {
	// Get the directory where the current executable is located
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get executable path: %w", err)
	}

	execDir := filepath.Dir(execPath)
	repoRoot := locateRepoRoot(execDir)

	var binaryName string

	if repoRoot != "" {
		cmdDir := filepath.Join(repoRoot, "cmd")

		switch appName {
		case "opencode-sessions":
			binaryName = filepath.Join(cmdDir, "opencode-sessions", "dist", "sessions-pane")
		case "opencode-messages":
			binaryName = filepath.Join(cmdDir, "opencode-messages", "dist", "messages-pane")
		case "opencode-input":
			binaryName = filepath.Join(cmdDir, "opencode-input", "dist", "input-pane")
		default:
			return "", fmt.Errorf("unknown app name: %s", appName)
		}
	} else {
		// Repo root could not be detected – fall back to legacy behavior and expect
		// binaries to be discoverable via PATH (handled by caller).
		return "", fmt.Errorf("repository root not found for %s binary", appName)
	}

	// Check if binary exists
	if _, err := os.Stat(binaryName); os.IsNotExist(err) {
		return "", fmt.Errorf("binary not found: %s", binaryName)
	}

	log.Printf("[DEBUG] Resolved binary path for %s: %s", appName, binaryName)
	return binaryName, nil
}

func locateRepoRoot(startDir string) string {
	dir := startDir
	for {
		if dir == "" || dir == "/" || dir == "." {
			break
		}

		if hasPanelDirs(dir) {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func hasPanelDirs(root string) bool {
	required := []string{
		filepath.Join(root, "cmd", "opencode-sessions"),
		filepath.Join(root, "cmd", "opencode-messages"),
		filepath.Join(root, "cmd", "opencode-input"),
	}
	for _, dir := range required {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// verifyPanelsRunning checks if panel applications are running
func (orch *TmuxOrchestrator) verifyPanelsRunning() bool {
	targets := []string{}

	if orch.layout != nil {
		seen := map[string]struct{}{}
		for _, panel := range orch.layout.Panels {
			paneTarget, ok := orch.panes[panel.ID]
			if !ok {
				continue
			}
			if _, exists := seen[paneTarget]; exists {
				continue
			}
			targets = append(targets, paneTarget)
			seen[paneTarget] = struct{}{}
		}
	}

	if len(targets) == 0 {
		sessionTarget := orch.sessionName + ":0"
		targets = []string{
			sessionTarget + ".0",
			sessionTarget + ".1",
			sessionTarget + ".2",
		}
	}

	panelsRunning := 0
	for idx, rawTarget := range targets {
		paneTarget := orch.normalizePaneTarget(rawTarget)
		cmd := exec.Command(orch.tmuxCommand, "list-panes", "-t", paneTarget, "-F", "#{pane_pid}")
		output, err := cmd.Output()
		if err == nil && len(output) > 0 {
			panelsRunning++
			log.Printf("[DEBUG] Pane %s is active (PID: %s)", paneTarget, strings.TrimSpace(string(output)))
			continue
		}
		log.Printf("[DEBUG] Pane %s (index %d) is not active or has no process", paneTarget, idx)
	}

	log.Printf("Panel verification: %d/%d panels are running", panelsRunning, len(targets))
	return panelsRunning > 0
}

// ReloadLayout reapplies the tmux layout from configuration without restarting running panels.
func (orch *TmuxOrchestrator) ReloadLayout() error {
	if orch.serverOnly {
		return fmt.Errorf("cannot reload layout in server-only mode")
	}
	if orch.configPath == "" {
		return fmt.Errorf("layout config path is not configured")
	}
	if !orch.sessionExists() {
		return fmt.Errorf("tmux session %s is not running", orch.sessionName)
	}

	orch.layoutMutex.Lock()
	defer orch.layoutMutex.Unlock()

	layoutCfg, err := tmuxconfig.LoadLayout(orch.configPath)
	if err != nil {
		return fmt.Errorf("failed to load layout config: %w", err)
	}

	oldWindowTarget := fmt.Sprintf("%s:0", orch.sessionName)
	oldWindowID, err := orch.getWindowID(oldWindowTarget)
	if err != nil {
		log.Printf("Warning: unable to determine current window id: %v", err)
		oldWindowID = ""
	}

	oldPaneMap := make(map[string]string, len(orch.panes))
	for id, pane := range orch.panes {
		oldPaneMap[id] = pane
	}

	// Create staging window that will host the new layout.
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "new-window", "-d", "-t", orch.sessionName, "-P", "-F", "#{window_id}")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create staging window: %w", err)
	}
	newWindowID := strings.TrimSpace(string(output))
	if newWindowID == "" {
		return fmt.Errorf("tmux did not return a window id for the staging window")
	}

	success := false
	defer func() {
		if !success {
			_ = exec.CommandContext(orch.ctx, orch.tmuxCommand, "kill-window", "-t", newWindowID).Run()
		}
	}()

	newRootPane, err := orch.resolvePaneID(newWindowID)
	if err != nil {
		return fmt.Errorf("failed to resolve staging window root pane: %w", err)
	}

	newPaneMap, err := orch.buildLayout(newRootPane, layoutCfg)
	if err != nil {
		return fmt.Errorf("failed to build layout in staging window: %w", err)
	}

	movedPanels := make(map[string]bool)
	for _, panel := range layoutCfg.Panels {
		newPaneID, ok := newPaneMap[panel.ID]
		if !ok || newPaneID == "" {
			continue
		}
		oldPaneID := oldPaneMap[panel.ID]
		if oldPaneID == "" || oldPaneID == newPaneID {
			continue
		}
		if err := orch.swapPaneContents(oldPaneID, newPaneID); err != nil {
			log.Printf("Warning: failed to move panel %s (%s -> %s): %v", panel.ID, oldPaneID, newPaneID, err)
			continue
		}
		movedPanels[panel.ID] = true
	}

	// Position the staging window as the primary window.
	target := fmt.Sprintf("%s:0", orch.sessionName)
	if err := exec.CommandContext(orch.ctx, orch.tmuxCommand, "move-window", "-s", newWindowID, "-t", target).Run(); err != nil {
		log.Printf("Warning: failed to move staging window to %s: %v", target, err)
	}

	// Remove the previous window to avoid leaving shells behind.
	if oldWindowID != "" && oldWindowID != newWindowID {
		if err := exec.CommandContext(orch.ctx, orch.tmuxCommand, "kill-window", "-t", oldWindowID).Run(); err != nil {
			log.Printf("Warning: failed to kill previous window %s: %v", oldWindowID, err)
		}
	}

	// Update orchestrator state.
	orch.layout = layoutCfg
	orch.panes = newPaneMap
	orch.logPaneAssignments("reload_layout", orch.panes)

	if err := orch.startMissingPanels(layoutCfg, newPaneMap, movedPanels); err != nil {
		log.Printf("Warning: failed to start all new panels after layout reload: %v", err)
	}

	// Ensure clients focus the refreshed window.
	if err := exec.CommandContext(orch.ctx, orch.tmuxCommand, "select-window", "-t", target).Run(); err != nil {
		log.Printf("Warning: failed to select refreshed window: %v", err)
	}

	success = true
	log.Printf("Layout reloaded successfully")
	return nil
}

// killTmuxSession kills the tmux session if it exists
func (orch *TmuxOrchestrator) killTmuxSession() error {
	cmd := exec.Command(orch.tmuxCommand, "kill-session", "-t", orch.sessionName)
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			// tmux returns exit status 1 when the session does not exist; treat as success.
			if exitErr.ExitCode() == 1 {
				return nil
			}
		}
		return err
	}
	return nil
}

// swapPaneContents swaps the processes between two tmux panes.
func (orch *TmuxOrchestrator) swapPaneContents(sourcePane, targetPane string) error {
	if strings.TrimSpace(sourcePane) == "" || strings.TrimSpace(targetPane) == "" {
		return fmt.Errorf("invalid pane ids for swap")
	}
	if sourcePane == targetPane {
		return nil
	}
	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, "swap-pane", "-s", sourcePane, "-t", targetPane)
	return cmd.Run()
}

// startMissingPanels launches applications for panels that did not inherit an existing process.
func (orch *TmuxOrchestrator) startMissingPanels(layoutCfg *tmuxconfig.Layout, panes map[string]string, moved map[string]bool) error {
	if layoutCfg == nil {
		return nil
	}

	envVars := map[string]string{
		"OPENCODE_SERVER": os.Getenv("OPENCODE_SERVER"),
		"OPENCODE_SOCKET": orch.socketPath,
	}

	var errs []string
	for _, panel := range layoutCfg.Panels {
		if moved[panel.ID] {
			continue
		}
		targetPane, ok := panes[panel.ID]
		if !ok || strings.TrimSpace(targetPane) == "" {
			continue
		}

		appName, err := resolvePanelAppName(panel)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", panel.ID, err))
			continue
		}

		if err := orch.startPanelApp(targetPane, appName, envVars); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", panel.ID, err))
			continue
		}
		log.Printf("Started panel %s after layout reload", panel.ID)
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// Shutdown implements the interfaces.OrchestratorControl interface.
// It triggers graceful shutdown of the orchestrator daemon.
// If cleanup is true, the tmux session will also be destroyed.
func (orch *TmuxOrchestrator) Shutdown(cleanup bool) error {
	log.Printf("Shutdown requested via IPC (cleanup=%v)", cleanup)

	// Store cleanup flag for use in Stop()
	orch.cleanupOnExit = cleanup

	// Trigger shutdown by canceling context
	// This will cause waitForShutdown() to return
	orch.cancel()

	return nil
}

// GetStatus returns the current status of the session
func (orch *TmuxOrchestrator) GetStatus() (*interfaces.SessionStatus, error) {
	// Calculate uptime
	uptime := time.Since(orch.startedAt)

	// Get panel status
	var panels []interfaces.PanelStatus
	orch.layoutMutex.Lock()
	if orch.layout != nil {
		for _, panel := range orch.layout.Panels {
			paneID := orch.panes[panel.ID]
			panelStatus := interfaces.PanelStatus{
				Name:      panel.ID,
				PaneID:    paneID,
				IsRunning: paneID != "",
			}
			panels = append(panels, panelStatus)
		}
	}
	orch.layoutMutex.Unlock()

	// Get client count using clientTracker if available
	clientCount := 0
	if orch.clientTracker != nil {
		count, _ := orch.clientTracker.GetConnectedClients()
		clientCount = count
	} else {
		// Fallback: try to count clients via tmux
		clients, err := orch.listTmuxClients()
		if err == nil {
			clientCount = len(clients)
		}
	}

	status := &interfaces.SessionStatus{
		SessionName: orch.sessionName,
		DaemonPID:   os.Getpid(),
		IsRunning:   orch.isRunning,
		Uptime:      uptime,
		StartedAt:   orch.startedAt,
		ClientCount: clientCount,
		Panels:      panels,
		SocketPath:  orch.socketPath,
		ConfigPath:  orch.configPath,
		Owner:       orch.owner,
	}

	return status, nil
}

// GetConnectedClients returns a list of connected tmux clients
func (orch *TmuxOrchestrator) GetConnectedClients() ([]interfaces.ClientInfo, error) {
	// Always use direct tmux query for now
	// ClientTracker's GetConnectedClientsInfo has different format
	return orch.listTmuxClients()
}

// Ping checks if the daemon is responsive
func (orch *TmuxOrchestrator) Ping() error {
	// Simple health check - if we can respond, we're alive
	if !orch.isRunning {
		return fmt.Errorf("orchestrator is not running")
	}
	return nil
}

// listTmuxClients queries tmux for connected clients
func (orch *TmuxOrchestrator) listTmuxClients() ([]interfaces.ClientInfo, error) {
	// Use tmux list-clients to get client information
	// Format: tty,pid,created,session,width,height
	cmd := exec.Command(orch.tmuxCommand, "list-clients", "-t", orch.sessionName,
		"-F", "#{client_tty},#{client_pid},#{client_created},#{client_session},#{client_width},#{client_height}")

	output, err := cmd.Output()
	if err != nil {
		// Session might not exist or no clients connected
		return []interfaces.ClientInfo{}, nil
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	clients := make([]interfaces.ClientInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) != 6 {
			continue
		}

		pid, _ := strconv.Atoi(parts[1])
		created, _ := strconv.ParseInt(parts[2], 10, 64)
		width, _ := strconv.Atoi(parts[4])
		height, _ := strconv.Atoi(parts[5])

		client := interfaces.ClientInfo{
			TTY:         parts[0],
			PID:         pid,
			ConnectedAt: time.Unix(created, 0),
			SessionName: parts[3],
			Width:       width,
			Height:      height,
		}

		clients = append(clients, client)
	}

	return clients, nil
}

// sendReloadLayoutCommand connects to the running orchestrator and requests a layout reload.
func sendReloadLayoutCommand(socketPath, sessionName string) error {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return fmt.Errorf("socket path is empty")
	}

	if _, err := os.Stat(socketPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("orchestrator is not running (socket %s not found)", socketPath)
		}
		return fmt.Errorf("failed to access socket %s: %w", socketPath, err)
	}

	panelID := fmt.Sprintf("controller-%s-%d", sessionName, time.Now().UnixNano())
	client := ipc.NewSocketClient(socketPath, panelID, "controller")
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to orchestrator socket: %w", err)
	}
	defer func() {
		_ = client.Disconnect()
	}()

	if err := client.SendOrchestratorCommand("reload_layout"); err != nil {
		return err
	}
	return nil
}

// attachExistingSession attaches to an already-running tmux session without modifying orchestrator state.
func (orch *TmuxOrchestrator) attachExistingSession() error {
	cmd := exec.Command(orch.tmuxCommand, "attach-session", "-t", orch.sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// attachToSession attaches to the tmux session
func (orch *TmuxOrchestrator) attachToSession() error {
	if !orch.isRunning {
		return fmt.Errorf("session is not running")
	}

	cmd := exec.Command(orch.tmuxCommand, "attach-session", "-t", orch.sessionName)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// detachAsDaemon re-executes the current process as a detached daemon.
// This is Stage 3.5: Daemon Detachment implementation.
//
// The parent process will:
// 1. Re-execute itself with the same arguments
// 2. Set Setsid=true to create a new session (detach from terminal)
// 3. Redirect stdin/stdout/stderr appropriately
// 4. Set OPENCODE_DAEMON_DETACHED=1 environment variable to prevent infinite loop
// 5. Exit immediately, allowing shell to return
//
// The child process will:
// 1. Detect OPENCODE_DAEMON_DETACHED=1 and skip detachment
// 2. Continue normal daemon startup (acquire lock, start IPC server, etc.)
// 3. Run as a true background daemon with PPID=1
func detachAsDaemon() error {
	// Get current executable path
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable path: %w", err)
	}

	// Prepare command with same arguments
	cmd := exec.Command(executable, os.Args[1:]...)

	// Add environment variable to mark child as detached
	cmd.Env = append(os.Environ(), "OPENCODE_DAEMON_DETACHED=1")

	// Configure process attributes for detachment
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true, // Create new session, detach from controlling terminal
	}

	// Redirect stdin to /dev/null
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("failed to open /dev/null for stdin: %w", err)
	}
	cmd.Stdin = devNull

	// Redirect stdout/stderr to log file for daemon process
	// Extract session name from arguments to create session-specific log
	sessionName := "opencode" // default
	for i, arg := range os.Args {
		if i > 0 && !strings.HasPrefix(arg, "-") && arg != "start" {
			sessionName = arg
			break
		}
	}

	pathMgr := paths.NewPathManager(sessionName)
	if err := pathMgr.EnsureDirectories(); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	var logFile *os.File
	var logPath string
	logPath = pathMgr.LogPath()
	logFile, err = os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// If we can't open log file, fall back to /dev/null
		// This ensures daemon can still start even if log directory is not writable
		log.Printf("Warning: failed to open log file %s: %v (falling back to /dev/null)", logPath, err)
		cmd.Stdout = devNull
		cmd.Stderr = devNull
	} else {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		// Don't close logFile - let child process inherit it
		// Parent will exit and the FD will remain open for the child
	}

	// Start the detached child process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start detached process: %w", err)
	}

	childPID := cmd.Process.Pid

	// Release the child process (don't wait for it)
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("failed to release child process: %w", err)
	}

	// Print success message to parent's stdout (user will see this)
	fmt.Printf("Daemon process started in background (PID: %d)\n", childPID)
	if logFile != nil {
		fmt.Printf("Logs: %s\n", logPath)
	}
	fmt.Printf("Use 'ps aux | grep %d' to verify it's running\n", childPID)

	return nil
}

// waitForShutdown waits for shutdown signals
// waitForShutdown waits for shutdown signals based on run mode
// - Foreground mode: SIGINT/SIGTERM trigger immediate shutdown
// - Daemon mode: SIGINT/SIGTERM are logged but ignored, only IPC shutdown works
func (orch *TmuxOrchestrator) waitForShutdown() {
	sigChan := make(chan os.Signal, 1)

	switch orch.runMode {
	case ModeForeground:
		// Foreground mode: respond to SIGTERM/SIGINT, trigger cleanup shutdown
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(sigChan)

		log.Printf("[Signal Handler] Running in Foreground mode - Ctrl+C will trigger shutdown")

		select {
		case <-orch.ctx.Done():
			// IPC shutdown request received via context cancellation
			log.Printf("[Signal Handler] Foreground mode: shutdown requested via IPC")
		case sig := <-sigChan:
			log.Printf("[Signal Handler] Foreground mode: received %s, triggering cleanup shutdown", sig)
		}

	case ModeDaemon:
		// Daemon mode: ignore SIGTERM/SIGINT (or log), only respond to IPC shutdown
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(sigChan)

		log.Printf("[Signal Handler] Running in Daemon mode - Ctrl+C will be ignored")
		log.Printf("[Signal Handler] Use 'tmuxcoder stop %s' to shutdown daemon", orch.sessionName)

		for {
			select {
			case <-orch.ctx.Done():
				// IPC shutdown request received via context cancellation
				log.Printf("[Signal Handler] Daemon mode: shutdown requested via IPC")
				return

			case sig := <-sigChan:
				// Log but don't act on terminal signals in daemon mode
				log.Printf("[Signal Handler] Daemon mode: received %s (ignored)", sig)
				log.Printf("[Signal Handler]   → Use 'tmuxcoder stop %s' to shutdown daemon", orch.sessionName)

				// Stage 1 Integration: Show connected client count if available
				if orch.clientTracker != nil {
					count, err := orch.clientTracker.GetConnectedClients()
					if err == nil {
						if count > 0 {
							log.Printf("[Signal Handler]   → %d client(s) currently connected", count)
						} else {
							log.Printf("[Signal Handler]   → No clients currently connected")
						}
					}
				}
			}
		}

	default:
		log.Printf("[Signal Handler] ERROR: Unknown run mode %v, defaulting to foreground behavior", orch.runMode)

		// Fallback to foreground behavior
		signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)
		defer signal.Stop(sigChan)

		sig := <-sigChan
		log.Printf("[Signal Handler] Unknown mode fallback: received %s, triggering shutdown", sig)
	}
}

// monitorHealth monitors the health of the system
func (orch *TmuxOrchestrator) monitorHealth() {
	// In server-only mode, don't monitor tmux session
	if !orch.serverOnly {
		// Start tmux session watcher in background
		go orch.watchTmuxSession()
	}

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-orch.ctx.Done():
			return
		case <-ticker.C:
			orch.performHealthCheck()
		}
	}
}

// watchTmuxSession polls tmux has-session to detect session exit quickly
func (orch *TmuxOrchestrator) watchTmuxSession() {
	for {
		select {
		case <-orch.ctx.Done():
			return
		default:
			cmd := exec.Command(orch.tmuxCommand, "has-session", "-t", orch.sessionName)
			if err := cmd.Run(); err != nil {
				log.Printf("Tmux session '%s' no longer exists, shutting down", orch.sessionName)
				// Release lock immediately so new process can start
				if orch.lock != nil {
					orch.lock.Release()
					orch.lock = nil
				}
				orch.cancel()
				return
			}
			time.Sleep(1 * time.Second)
		}
	}
}

// performHealthCheck checks the health of all components
func (orch *TmuxOrchestrator) performHealthCheck() {
	// Check sync manager health
	if orch.syncManager != nil && !orch.syncManager.IsHealthy() {
		log.Printf("Warning: Sync manager is not healthy")
	}

	// Check IPC server health
	if orch.ipcServer != nil && !orch.ipcServer.IsRunning() {
		log.Printf("Warning: IPC server is not running")
	}

	// Check tmux session - exit if tmux session is gone (skip in server-only mode)
	if !orch.serverOnly && orch.isRunning && !orch.isTmuxSessionRunning() {
		log.Printf("Tmux session '%s' no longer exists, shutting down orchestrator", orch.sessionName)
		orch.cancel() // Trigger graceful shutdown
	}
}

// isTmuxSessionRunning checks if the tmux session is still running
func (orch *TmuxOrchestrator) isTmuxSessionRunning() bool {
	cmd := exec.Command(orch.tmuxCommand, "has-session", "-t", orch.sessionName)
	return cmd.Run() == nil
}

func resolveStorageRoot() (string, error) {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return filepath.Join(base, "opencode", "storage"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "opencode", "storage"), nil
}

func (orch *TmuxOrchestrator) resizePane(target string, axis string, value string) error {
	val := strings.TrimSpace(value)
	if val == "" {
		return nil
	}

	args := []string{"resize-pane", "-t", target}

	if strings.HasSuffix(val, "%") {
		pct := strings.TrimSuffix(val, "%")
		pct = strings.TrimSpace(pct)
		if pct == "" {
			return fmt.Errorf("invalid percentage pane size: %q", value)
		}
		args = append(args, "-p", pct)
		cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, args...)
		return cmd.Run()
	}

	if axis == "x" {
		args = append(args, "-x", val)
	}
	if axis == "y" {
		args = append(args, "-y", val)
	}
	if len(args) != 4 {
		return fmt.Errorf("invalid axis for resize-pane: %q", axis)
	}

	cmd := exec.CommandContext(orch.ctx, orch.tmuxCommand, args...)
	return cmd.Run()
}

// printStatus prints the current status of the orchestrator
func (orch *TmuxOrchestrator) printStatus() {
	fmt.Printf("OpenCode Tmux Orchestrator Status:\n")
	fmt.Printf("  Session Name: %s\n", orch.sessionName)
	fmt.Printf("  Socket Path: %s\n", orch.socketPath)
	fmt.Printf("  State Path: %s\n", orch.statePath)
	fmt.Printf("  Running: %v\n", orch.isRunning)

	if orch.ipcServer != nil {
		fmt.Printf("  IPC Server: %v\n", orch.ipcServer.IsRunning())
		connections := orch.ipcServer.GetConnections()
		fmt.Printf("  Connected Panels: %d\n", len(connections))
		for _, conn := range connections {
			fmt.Printf("    - %s (%s)\n", conn.PanelID, conn.PanelType)
		}
	}

	if orch.syncManager != nil {
		metrics := orch.syncManager.GetMetrics()
		fmt.Printf("  State Updates: %d (%.1f%% success)\n",
			metrics.TotalUpdates, metrics.GetSuccessRate())
		fmt.Printf("  State Saves: %d (%.1f%% success)\n",
			metrics.TotalSaves, metrics.GetSaveSuccessRate())
	}
}

func firstPositionalArg(args []string) (string, bool) {
	for idx, arg := range args {
		if arg == "--" {
			if idx+1 < len(args) {
				next := args[idx+1]
				if next != "" {
					return next, true
				}
			}
			return "", false
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		if arg != "" {
			return arg, true
		}
	}
	return "", false
}

func sanitizeLogComponent(name string) string {
	if name == "" {
		return "opencode"
	}
	var builder strings.Builder
	builder.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "opencode"
	}
	return builder.String()
}

// runLegacyMode runs the original main function logic (backward compatibility)
func runLegacyMode() {
	// Configure logging to file
	logFileHomeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}
	logDir := filepath.Join(logFileHomeDir, ".opencode")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Fatalf("Failed to create log directory: %v", err)
	}

	// Parse session name early for logging
	sessionName := "opencode"
	if positional, ok := firstPositionalArg(os.Args[1:]); ok {
		sessionName = positional
	}
	// Note: We can't fully load config yet as we haven't parsed flags,
	// but we need to setup logging early. We'll use the CLI arg or default
	// for the log name. If it changes later (e.g. from config), the log
	// name will remain as started, which is acceptable.

	logPath := filepath.Join(logDir, fmt.Sprintf("tmux-%s.log", sanitizeLogComponent(sessionName)))
	if logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		log.SetOutput(logFile)
		log.SetFlags(log.LstdFlags | log.Lshortfile)
		defer logFile.Close()
	} else {
		fmt.Fprintf(os.Stderr, "Warning: failed to open log file %s: %v (continuing with stderr logging)\n", logPath, err)
	}

	// Parse command line arguments
	var serverOnly bool
	var reuseSessionFlag bool
	var forceNewSessionFlag bool
	var attachOnlyFlag bool
	var reloadLayoutFlag bool
	flag.BoolVar(&serverOnly, "server-only", false, "Only start IPC server without panels")
	flag.BoolVar(&reuseSessionFlag, "reuse-session", false, "Reuse an existing tmux session without prompting")
	flag.BoolVar(&forceNewSessionFlag, "force-new-session", false, "Force creation of a new tmux session, replacing any existing session")
	flag.BoolVar(&attachOnlyFlag, "attach-only", false, "Attach to an existing tmux session and exit without reconfiguring panels")
	flag.BoolVar(&reloadLayoutFlag, "reload-layout", false, "Reload the tmux layout without restarting panel processes")

	// Stage 3: Signal handling mode flags
	var daemonFlag bool
	var foregroundFlag bool
	flag.BoolVar(&daemonFlag, "daemon", false, "Run in daemon mode (Ctrl+C ignored, use IPC to shutdown)")
	flag.BoolVar(&foregroundFlag, "foreground", false, "Run in foreground mode (Ctrl+C triggers shutdown) [default]")

	flag.Parse()

	if reuseSessionFlag && forceNewSessionFlag {
		log.Fatal("cannot specify both --reuse-session and --force-new-session")
	}
	if attachOnlyFlag && forceNewSessionFlag {
		log.Fatal("cannot combine --attach-only with --force-new-session")
	}
	if attachOnlyFlag && serverOnly {
		log.Fatal("cannot combine --attach-only with --server-only")
	}
	if reloadLayoutFlag && serverOnly {
		log.Fatal("cannot combine --reload-layout with --server-only")
	}
	if reloadLayoutFlag && forceNewSessionFlag {
		log.Fatal("cannot combine --reload-layout with --force-new-session")
	}
	if reloadLayoutFlag && attachOnlyFlag {
		log.Fatal("cannot combine --reload-layout with --attach-only")
	}

	// Stage 3: Validate and determine run mode
	var runMode RunMode
	if daemonFlag && foregroundFlag {
		log.Printf("Warning: both --daemon and --foreground specified, defaulting to foreground mode")
		runMode = ModeForeground
	} else if daemonFlag {
		runMode = ModeDaemon
		log.Printf("Starting in Daemon mode (Ctrl+C will be ignored)")
	} else {
		// Default to foreground for backward compatibility
		runMode = ModeForeground
		if foregroundFlag {
			log.Printf("Starting in Foreground mode (explicit)")
		}
	}

	sessionOverride := false
	if flag.NArg() > 0 {
		sessionName = flag.Arg(0)
		sessionOverride = true
	} else if sessionName != "opencode" {
		sessionOverride = true
	}

	// Stage 3.5: Daemon Detachment with Pre-Lock Check
	// If daemon mode is enabled and not already detached, check lock and re-execute as detached process
	if runMode == ModeDaemon && os.Getenv("OPENCODE_DAEMON_DETACHED") == "" {
		// Pre-detachment lock check to prevent duplicate daemons
		// This happens BEFORE detachment to avoid race conditions
		pathMgr := paths.NewPathManager(sessionName)
		if err := pathMgr.EnsureDirectories(); err != nil {
			log.Fatalf("Failed to create directories: %v", err)
		}

		// Check if another instance is already running
		if pid, running := session.CheckLock(pathMgr.PIDPath()); running {
			fmt.Fprintf(os.Stderr, "Error: Orchestrator already running for session '%s' (PID: %d)\n", sessionName, pid)
			fmt.Fprintf(os.Stderr, "PID file: %s\n", pathMgr.PIDPath())
			fmt.Fprintf(os.Stderr, "\nTo stop the existing daemon:\n")
			fmt.Fprintf(os.Stderr, "  kill -9 %d\n", pid)
			fmt.Fprintf(os.Stderr, "Or use: tmuxcoder stop %s\n", sessionName)
			os.Exit(1)
		}

		// Lock check passed, proceed with detachment
		log.Printf("[Daemon] Detaching from terminal...")
		if err := detachAsDaemon(); err != nil {
			log.Fatalf("[Daemon] Failed to detach: %v", err)
		}
		// Parent process exits here - shell returns to user
		// Child process continues below with OPENCODE_DAEMON_DETACHED=1
		os.Exit(0)
	}

	// If we reach here, either:
	// 1. Running in foreground mode (runMode == ModeForeground), OR
	// 2. Already detached as daemon child (OPENCODE_DAEMON_DETACHED=1)

	// If this is the detached daemon child, redirect log output to file
	if runMode == ModeDaemon && os.Getenv("OPENCODE_DAEMON_DETACHED") == "1" {
		pathMgr := paths.NewPathManager(sessionName)
		logPath := pathMgr.LogPath()
		logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(logFile)
			log.Printf("=== Daemon process started (PID: %d) ===", os.Getpid())
		} else {
			log.Printf("Warning: failed to open log file %s: %v", logPath, err)
		}
	}

	// Get configuration from environment
	serverURL := os.Getenv("OPENCODE_SERVER")
	if serverURL == "" && !serverOnly {
		log.Fatal("OPENCODE_SERVER environment variable not set")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		log.Fatal("Failed to get home directory:", err)
	}

	configPath := os.Getenv("OPENCODE_TMUX_CONFIG")
	if configPath == "" {
		configPath = filepath.Join(homeDir, ".opencode", "tmux.yaml")
	}

	sessionCfg, err := tmuxconfig.LoadSession(configPath)
	if err != nil {
		log.Fatalf("Failed to load tmux session config: %v", err)
	}

	// === Per-Session Architecture: Use session name from command line ===
	// The sessionName variable (from flag.Arg(0)) is the target tmux session name
	// We'll use this to create per-session isolated paths

	// Environment variables for explicit path override (optional)
	envSocketPath := os.Getenv("OPENCODE_SOCKET")
	envStatePath := os.Getenv("OPENCODE_STATE")

	var socketPath, statePath string
	var lock *session.SessionLock

	// Create path manager based on the target tmux session name
	pathMgr := paths.NewPathManager(sessionName)
	log.Printf("Managing tmux session: %s", sessionName)

	// Ensure all necessary directories exist
	if err := pathMgr.EnsureDirectories(); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	// Cleanup stale files (files older than 7 days from zombie processes)
	if err := pathMgr.CleanupStaleFiles(7 * 24 * time.Hour); err != nil {
		log.Printf("Warning: failed to cleanup stale files: %v", err)
	}

	// Determine socket path early (needed for reload-layout command)
	if envSocketPath != "" {
		socketPath = envSocketPath
		log.Printf("Socket path (from env): %s", socketPath)
	} else {
		socketPath = pathMgr.SocketPath()
		log.Printf("Socket path (per-session): %s", socketPath)
	}

	// Handle reload-layout command BEFORE acquiring lock
	// (orchestrator is already running, so we send IPC message and exit)
	if reloadLayoutFlag {
		if !sessionOverride {
			name := strings.TrimSpace(sessionCfg.Session.Name)
			if name != "" {
				sessionName = name
			}
		}
		if err := sendReloadLayoutCommand(socketPath, sessionName); err != nil {
			fmt.Fprintf(os.Stderr, "Reload layout failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Reload layout command sent successfully.")
		return
	}

	// Prevent duplicate startup: acquire session lock
	lock, err = session.AcquireLock(pathMgr.PIDPath())
	if err != nil {
		if err == session.ErrAlreadyRunning {
			log.Fatalf("Orchestrator already running for tmux session '%s'\nPID file: %s",
				sessionName, pathMgr.PIDPath())
		}
		log.Fatalf("Failed to acquire lock: %v", err)
	}
	defer func() {
		if lock != nil {
			lock.Release()
		}
	}()
	log.Printf("Lock acquired: %s", pathMgr.PIDPath())

	if envStatePath != "" {
		statePath = envStatePath
		log.Printf("State path (from env): %s", statePath)
	} else {
		statePath = pathMgr.StatePath()
		log.Printf("State path (per-session): %s", statePath)
	}

	layoutCfg, err := tmuxconfig.LoadLayout(configPath)
	if err != nil {
		log.Fatalf("Failed to load tmux layout config: %v", err)
	}

	if !sessionOverride {
		name := strings.TrimSpace(sessionCfg.Session.Name)
		if name != "" {
			sessionName = name
		}
	}

	// Create HTTP client (only if not in server-only mode)
	var httpClient *opencode.Client
	if !serverOnly {
		httpClient = opencode.NewClient(option.WithBaseURL(serverURL))
	}

	// Initialize theme
	if err := theme.LoadThemesFromJSON(); err != nil {
		log.Fatal("Failed to load themes:", err)
	}
	if err := theme.SetTheme("opencode"); err != nil {
		log.Fatal("Failed to set theme:", err)
	}

	// Create orchestrator
	orchestrator := NewTmuxOrchestrator(sessionName, socketPath, statePath, serverURL, httpClient, serverOnly, layoutCfg, reuseSessionFlag, forceNewSessionFlag, attachOnlyFlag, configPath, runMode)
	orchestrator.lock = lock

	if err := orchestrator.prepareExistingSession(); err != nil {
		log.Fatal(err)
	}

	if orchestrator.attachOnly {
		if err := orchestrator.attachExistingSession(); err != nil {
			log.Fatalf("Failed to attach to tmux session: %v", err)
		}
		return
	}

	if serverOnly {
		log.Printf("Starting in server-only mode - IPC server only, no panels")
	}

	// Initialize
	if err := orchestrator.Initialize(); err != nil {
		log.Fatal("Failed to initialize orchestrator:", err)
	}

	// Start tmux session
	if err := orchestrator.Start(); err != nil {
		log.Fatal("Failed to start tmux session:", err)
	}

	// Start health monitoring
	go orchestrator.monitorHealth()

	// Print status
	orchestrator.printStatus()

	// Attach to session if stdin is a terminal and not in server-only mode.
	// Keep the orchestrator running even after the caller detaches so other
	// clients can connect to the same tmux session.
	if isTerminal() && !serverOnly {
		go func() {
			log.Printf("Attaching to tmux session...")
			if err := orchestrator.attachToSession(); err != nil {
				log.Printf("Failed to attach to session: %v", err)
				return
			}
			log.Printf("Tmux session detached; orchestrator continues running until interrupted")
		}()
	} else if serverOnly {
		log.Printf("Server-only mode: waiting for shutdown signal...")
	}

	log.Printf("Tmux orchestrator running; press Ctrl+C to stop.")
	orchestrator.waitForShutdown()

	// Cleanup
	if err := orchestrator.Stop(); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}
}

// Stage 6: Session recovery and health management methods

// recoverExistingSession checks and recovers panes in an existing session
// This is called when reusing a session to ensure all panes are healthy
func (orch *TmuxOrchestrator) recoverExistingSession() error {
	if orch.healthChecker == nil {
		log.Printf("[Reuse] Health checker not initialized, skipping recovery")
		return nil
	}

	log.Printf("[Reuse] Checking health of existing session: %s", orch.sessionName)

	// Get all pane targets from current layout
	orch.layoutMutex.Lock()
	paneTargets := make([]string, 0, len(orch.panes))
	for _, target := range orch.panes {
		paneTargets = append(paneTargets, target)
	}
	orch.layoutMutex.Unlock()

	if len(paneTargets) == 0 {
		log.Printf("[Reuse] No panes to check")
		return nil
	}

	// Check health of all panes
	healthStatus := orch.healthChecker.CheckAllPanesHealth(paneTargets)

	// Create environment checker
	envChecker := supervision.NewEnvChecker(orch.tmuxCommand, map[string]string{
		"OPENCODE_SOCKET": orch.socketPath,
	})

	// Process each pane based on health status
	for paneTarget, health := range healthStatus {
		log.Printf("[Reuse] Pane %s: %s", paneTarget, health)

		switch health {
		case supervision.PaneHealthy:
			// Check if environment variables are stale
			if envChecker.NeedsEnvUpdate(paneTarget) {
				log.Printf("[Reuse] Pane %s has stale environment, will restart", paneTarget)
				if err := orch.killPaneProcess(paneTarget); err != nil {
					log.Printf("[Reuse] Failed to kill pane %s process: %v", paneTarget, err)
				}
			} else {
				log.Printf("[Reuse] Pane %s is healthy with correct environment", paneTarget)
			}

		case supervision.PaneDead:
			log.Printf("[Reuse] Pane %s is dead, supervisor will restart it", paneTarget)
			// The monitor goroutine will handle restart

		case supervision.PaneZombie:
			log.Printf("[Reuse] Pane %s is zombie, killing it", paneTarget)
			if err := orch.killZombiePane(paneTarget); err != nil {
				log.Printf("[Reuse] Failed to kill zombie pane %s: %v", paneTarget, err)
			}

		case supervision.PaneMissing:
			log.Printf("[Reuse] Pane %s is missing, will be recreated", paneTarget)
			// The session configuration process will handle this
		}
	}

	return nil
}

// killPaneProcess gracefully terminates the process in a pane
func (orch *TmuxOrchestrator) killPaneProcess(paneTarget string) error {
	// Send Ctrl+C to the process
	cmd := exec.Command(orch.tmuxCommand, "send-keys", "-t", paneTarget, "C-c")
	if err := cmd.Run(); err != nil {
		log.Printf("[Kill] Failed to send C-c to pane %s: %v", paneTarget, err)
	}

	// Wait briefly for graceful shutdown
	time.Sleep(500 * time.Millisecond)

	// Check if process is still alive
	alive, err := orch.isPaneAlive(paneTarget)
	if err != nil {
		return fmt.Errorf("failed to check pane status: %w", err)
	}

	// If still alive, force kill with respawn-pane
	if alive {
		log.Printf("[Kill] Process in pane %s still alive, forcing respawn", paneTarget)
		cmd = exec.Command(orch.tmuxCommand, "respawn-pane", "-k", "-t", paneTarget, "exit 0")
		return cmd.Run()
	}

	return nil
}

// killZombiePane forces a zombie pane to be respawned
func (orch *TmuxOrchestrator) killZombiePane(paneTarget string) error {
	cmd := exec.Command(orch.tmuxCommand, "respawn-pane", "-k", "-t", paneTarget, "true")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to respawn zombie pane: %w", err)
	}
	log.Printf("[Kill] Zombie pane %s respawned", paneTarget)
	return nil
}

// setTmuxStatusBarWarning displays a warning message in the tmux status bar
func (orch *TmuxOrchestrator) setTmuxStatusBarWarning(message string) {
	cmd := exec.Command(orch.tmuxCommand, "set", "-t", orch.sessionName,
		"status-right", fmt.Sprintf("#[fg=yellow,bold]%s", message))
	if err := cmd.Run(); err != nil {
		log.Printf("[StatusBar] Failed to set warning: %v", err)
	}
}

// clearTmuxStatusBar clears the tmux status bar warning
func (orch *TmuxOrchestrator) clearTmuxStatusBar() {
	cmd := exec.Command(orch.tmuxCommand, "set", "-t", orch.sessionName, "status-right", "")
	if err := cmd.Run(); err != nil {
		log.Printf("[StatusBar] Failed to clear status bar: %v", err)
	}
}

// main is the entry point that dispatches to subcommands or legacy mode
func main() {
	// Setup bridge function for start command to call legacy mode
	commands.RunLegacyWithArgs = runLegacyModeWithArgs

	// Check if subcommand is used
	if len(os.Args) >= 2 {
		subcommand := os.Args[1]
		knownCommands := []string{"start", "attach", "detach", "stop", "status", "list", "help", "version"}

		// Check if first argument is a known subcommand
		if contains(knownCommands, subcommand) {
			executeSubcommand(subcommand, os.Args[2:])
			return
		}
	}

	// Backward compatibility: run legacy mode if no subcommand
	runLegacyMode()
}

// contains checks if a string is in a slice
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// executeSubcommand dispatches to the appropriate subcommand handler
func executeSubcommand(subcommand string, args []string) {
	// Import commands package functions
	var err error

	switch subcommand {
	case "start":
		// Use new CmdStart implementation
		err = commands.CmdStart(args)

	case "attach":
		err = commands.CmdAttach(args)

	case "detach":
		err = commands.CmdDetach(args)

	case "stop":
		err = commands.CmdStop(args)

	case "status":
		err = commands.CmdStatus(args)

	case "list":
		err = commands.CmdList(args)

	case "help":
		printHelp()

	case "version":
		printVersion()

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", subcommand)
		fmt.Fprintf(os.Stderr, "Run 'opencode-tmux help' for usage.\n")
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// translateStartArgs maps new CLI flag aliases to the legacy flags that
// runLegacyMode expects (e.g. --reuse -> --reuse-session).
func translateStartArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	flags := make([]string, 0, len(args))
	positionals := make([]string, 0, 1)
	trailing := []string{}
	mapped := func(arg string) string {
		switch arg {
		case "--reuse":
			return "--reuse-session"
		case "--force", "--force-new":
			return "--force-new-session"
		default:
			return arg
		}
	}

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]
		if arg == "--" {
			trailing = append(trailing, args[idx:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && arg != "-" {
			flags = append(flags, mapped(arg))
			continue
		}
		positionals = append(positionals, arg)
	}

	result := make([]string, 0, len(args))
	result = append(result, flags...)
	result = append(result, positionals...)
	result = append(result, trailing...)
	return result
}

// runLegacyModeWithArgs re-runs the legacy starter after re-writing os.Args
// so that the "start" subcommand token is transparent to the legacy parser.
func runLegacyModeWithArgs(args []string) error {
	originalArgs := os.Args
	newArgs := append([]string{os.Args[0]}, args...)
	os.Args = newArgs
	defer func() {
		os.Args = originalArgs
	}()

	// runLegacyMode calls log.Fatal on errors, so we never actually return an error
	// This is kept for interface compatibility
	runLegacyMode()
	return nil
}

// printHelp shows usage information
func printHelp() {
	fmt.Println("OpenCode Tmux Orchestrator - Manage collaborative tmux sessions")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  opencode-tmux <command> [options] [session-name]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  start      Start orchestrator daemon and tmux session")
	fmt.Println("  attach     Connect to existing tmux session")
	fmt.Println("  detach     Disconnect from current session (daemon continues)")
	fmt.Println("  stop       Stop orchestrator daemon")
	fmt.Println("  status     View session status")
	fmt.Println("  list       List all running sessions")
	fmt.Println("  help       Show this help message")
	fmt.Println("  version    Show version information")
	fmt.Println()
	fmt.Println("Legacy Usage (backward compatible):")
	fmt.Println("  opencode-tmux [options] [session-name]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  opencode-tmux start mysession")
	fmt.Println("  opencode-tmux start mysession --server-only --daemon")
	fmt.Println("  opencode-tmux attach mysession")
	fmt.Println("  opencode-tmux stop mysession --cleanup")
	fmt.Println("  opencode-tmux status mysession")
	fmt.Println()
	fmt.Println("For more information on a specific command:")
	fmt.Println("  opencode-tmux <command> --help")
}

// printVersion shows version information
func printVersion() {
	fmt.Println("OpenCode Tmux Orchestrator")
	fmt.Println("Version: 2.0.0-stage4")
	fmt.Println("Build: Stage 4 - CLI Subcommands")
}

// startSSEClient starts the Server-Sent Events client for real-time updates
func (orch *TmuxOrchestrator) startSSEClient() error {
	// Replace raw SSE parsing with typed SDK streaming
	log.Printf("Starting typed event stream client via SDK")

	go func() {
		for {
			select {
			case <-orch.ctx.Done():
				log.Printf("Event stream stopping due to context cancellation")
				return
			default:
				// Open a new stream filtered by project directory
				stream := orch.httpClient.Event.ListStreaming(
					orch.ctx,
					opencode.EventListParams{},
				)

				for stream.Next() {
					evt := stream.Current()
					orch.handleTypedEvent(evt)
				}

				// Capture error, close, and backoff before retrying
				if err := stream.Err(); err != nil {
					log.Printf("Event stream error: %v", err)
				}
				_ = stream.Close()

				// Check if we're shutting down before retrying
				select {
				case <-orch.ctx.Done():
					log.Printf("Event stream stopping due to context cancellation")
					return
				default:
					log.Printf("Event stream closed; retrying in 5 seconds...")
					select {
					case <-orch.ctx.Done():
						return
					case <-time.After(5 * time.Second):
					}
				}
			}
		}
	}()

	return nil
}

// handleTypedEvent processes typed SDK events
func (orch *TmuxOrchestrator) handleTypedEvent(evt opencode.EventListResponse) {
	switch evt.Type {
	case opencode.EventListResponseTypeMessageUpdated:
		// Cast to typed union variant
		uni := evt.AsUnion()
		if v, ok := uni.(opencode.EventListResponseEventMessageUpdated); ok {
			info := v.Properties.Info

			// Create or refresh local message metadata; content will be built by parts
			msg := types.MessageInfo{
				ID:        info.ID,
				SessionID: info.SessionID,
				Type:      string(info.Role),
				Content:   "",
				Timestamp: time.Now(),
				// User messages are immediately completed; assistant starts pending
				Status: func() string {
					if info.Role == opencode.MessageRoleUser {
						return "completed"
					}
					return "pending"
				}(),
			}
			// Avoid duplicate additions if multiple message.updated events arrive for same ID
			exists := false
			currentStatus := ""
			st := orch.syncManager.GetState()
			for _, m := range st.Messages {
				if m.ID == msg.ID {
					exists = true
					currentStatus = m.Status
					break
				}
			}
			if exists {
				// Only update status if the message is not already completed
				// This prevents message.updated events from overwriting the "completed" status
				// that was set by step-finish events
				if currentStatus != "completed" {
					desiredStatus := "pending"
					if info.Role == opencode.MessageRoleUser {
						desiredStatus = "completed"
					}
					if err := orch.syncManager.UpdateMessage(msg.ID, "", desiredStatus, "sse"); err != nil {
						log.Printf("[SSE] Failed to refresh message status for %s: %v", msg.ID, err)
					} else {
						log.Printf("[SSE] Message metadata exists; status refreshed: %s -> %s", msg.ID, desiredStatus)
					}
				} else {
					log.Printf("[SSE] Message metadata exists but already completed; skipping status update: %s", msg.ID)
				}
			} else {
				// Track message role for later use when parts arrive
				orch.messageRolesMu.Lock()
				orch.messageRoles[msg.ID] = msg.Type
				orch.messageRolesMu.Unlock()

				// Skip user messages with no content - they'll be added when content arrives via message.part.updated
				if info.Role == opencode.MessageRoleUser && msg.Content == "" {
					log.Printf("[SSE] Skipping empty user message; waiting for content: %s", msg.ID)
				} else {
					if err := orch.syncManager.AddMessage(msg, "sse"); err != nil {
						log.Printf("[SSE] Failed to add message: %v", err)
					} else {
						log.Printf("[SSE] Message metadata added: %s", msg.ID)
					}
				}
			}
		} else {
			log.Printf("[SSE] Unexpected union type for message.updated")
		}

	case opencode.EventListResponseTypeMessagePartUpdated:
		uni := evt.AsUnion()
		if v, ok := uni.(opencode.EventListResponseEventMessagePartUpdated); ok {
			part := v.Properties.Part
			// Skip reasoning/analysis parts from streaming into visible assistant content
			if strings.EqualFold(string(part.Type), "reasoning") || strings.EqualFold(string(part.Type), "thinking") || strings.EqualFold(string(part.Type), "analysis") {
				log.Printf("[SSE] part.skipped id=%s type=%s len=%d (reasoning/thinking)", part.MessageID, part.Type, len(part.Text))
				return
			}
			// Mark message as completed when step-finish part arrives
			if part.Type == opencode.PartTypeStepFinish {
				if err := orch.syncManager.UpdateMessage(part.MessageID, "", "completed", "sse"); err != nil {
					log.Printf("[SSE] Failed to mark completed for message %s: %v", part.MessageID, err)
				} else {
					log.Printf("[SSE] message.completed id=%s", part.MessageID)
				}
				return
			}
			// Append text to message content
			messageID := part.MessageID
			appended := part.Text
			if appended == "" {
				// nothing to append
				return
			}

			// Get current content for the message and append
			st := orch.syncManager.GetState()
			cur := ""
			exists := false
			for _, m := range st.Messages {
				if m.ID == messageID {
					cur = m.Content
					exists = true
					break
				}
			}
			// If message doesn't exist yet (part arrived before metadata), create it
			if !exists {
				// Determine message type from tracked roles or default to assistant
				messageType := "assistant"
				messageStatus := "pending"
				orch.messageRolesMu.Lock()
				if role, ok := orch.messageRoles[messageID]; ok {
					messageType = role
					if messageType == "user" {
						messageStatus = "completed"
					}
				}
				orch.messageRolesMu.Unlock()

				placeholder := types.MessageInfo{
					ID:        messageID,
					SessionID: part.SessionID,
					Type:      messageType,
					Content:   "",
					Timestamp: time.Now(),
					Status:    messageStatus,
				}
				if err := orch.syncManager.AddMessage(placeholder, "sse"); err != nil {
					log.Printf("[SSE] Failed to create placeholder message %s: %v", messageID, err)
				} else {
					log.Printf("[SSE] Created placeholder message %s with type %s", messageID, messageType)
				}
			}
			// Log diagnostic info before merging
			prefixReplace := strings.HasPrefix(appended, cur)
			// Compute overlap length (suffix of current vs prefix of appended)
			max := len(cur)
			if len(appended) < max {
				max = len(appended)
			}
			overlap := 0
			for i := 1; i <= max; i++ {
				if strings.HasSuffix(cur, appended[:i]) {
					overlap = i
				}
			}
			log.Printf("[SSE] part.updated id=%s type=%s cur_len=%d app_len=%d prefix_replace=%t overlap=%d app_preview=%.80q",
				messageID, part.Type, len(cur), len(appended), prefixReplace, overlap, appended)

			// Merge streaming text intelligently to avoid duplicated content
			newContent := mergeStreamingText(cur, appended)
			log.Printf("[SSE] part.merge   id=%s new_len=%d new_preview=%.80q", messageID, len(newContent), newContent)

			if err := orch.syncManager.UpdateMessage(messageID, newContent, "", "sse"); err != nil {
				log.Printf("[SSE] Failed to append part to message %s: %v", messageID, err)
			}
		} else {
			log.Printf("[SSE] Unexpected union type for message.part.updated")
		}

	case opencode.EventListResponseTypeMessageRemoved:
		uni := evt.AsUnion()
		if v, ok := uni.(opencode.EventListResponseEventMessageRemoved); ok {
			// Construct a deletion update with optimistic version check
			upd := types.StateUpdate{
				ID:              fmt.Sprintf("del_%s_%d", v.Properties.MessageID, time.Now().UnixNano()),
				Type:            types.MessageDeleted,
				ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
				Payload:         types.MessageDeletePayload{MessageID: v.Properties.MessageID},
				SourcePanel:     "sse",
				Timestamp:       time.Now(),
			}
			if err := orch.syncManager.UpdateWithVersionCheck(upd); err != nil {
				log.Printf("[SSE] Failed to delete message %s: %v", v.Properties.MessageID, err)
			}
		} else {
			log.Printf("[SSE] Unexpected union type for message.removed")
		}

	case opencode.EventListResponseTypeSessionCompacted:
		uni := evt.AsUnion()
		if v, ok := uni.(opencode.EventListResponseEventSessionCompacted); ok {
			if err := orch.handleSessionCompactedEvent(v.Properties.SessionID); err != nil {
				log.Printf("[SSE] Failed to process session.compacted event: %v", err)
			}
		} else {
			log.Printf("[SSE] Unexpected union type for session.compacted")
		}

	case opencode.EventListResponseTypeSessionDeleted:
		uni := evt.AsUnion()
		if v, ok := uni.(opencode.EventListResponseEventSessionDeleted); ok {
			// Construct a session deletion update
			upd := types.StateUpdate{
				ID:              fmt.Sprintf("del_session_%s_%d", v.Properties.Info.ID, time.Now().UnixNano()),
				Type:            types.SessionDeleted,
				ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
				Payload:         types.SessionDeletePayload{SessionID: v.Properties.Info.ID},
				SourcePanel:     "sse",
				Timestamp:       time.Now(),
			}
			// Apply the update without version check to make deletion idempotent
			if err := orch.syncManager.UpdateWithVersionCheck(upd); err != nil {
				log.Printf("[SSE] Failed to delete session %s: %v", v.Properties.Info.ID, err)
			} else {
				log.Printf("[SSE] Session deleted from state: %s", v.Properties.Info.ID)
			}
		} else {
			log.Printf("[SSE] Unexpected union type for session.deleted")
		}

	default:
		// Log unhandled event types for future mapping
		log.Printf("[SSE] Unhandled event type: %s", string(evt.Type))
	}
}

// handleSSEEvent processes incoming SSE events
func (orch *TmuxOrchestrator) handleSSEEvent(data string) {
	log.Printf("[SSE] Received event: %s", data)

	type envelope struct {
		Type       string          `json:"type"`
		Properties json.RawMessage `json:"properties"`
	}

	var env envelope
	if err := json.Unmarshal([]byte(data), &env); err != nil {
		log.Printf("[SSE] Failed to decode event envelope: %v", err)
		return
	}

	switch env.Type {
	case "session.idle":
		// properties: { sessionID: string }
		type sessionIdleProps struct {
			SessionID string `json:"sessionID"`
		}
		var props sessionIdleProps
		if err := json.Unmarshal(env.Properties, &props); err != nil {
			log.Printf("[SSE] Failed to decode session.idle properties: %v", err)
			return
		}
		log.Printf("[SSE] Session %s is now idle", props.SessionID)
		// For now, just log the event. Could be used for UI state updates in the future.

	case "session.updated":
		// properties: { info: Session }
		type sessionUpdatedProps struct {
			Info opencode.Session `json:"info"`
		}
		var props sessionUpdatedProps
		if err := json.Unmarshal(env.Properties, &props); err != nil {
			log.Printf("[SSE] Failed to decode session.updated properties: %v", err)
			return
		}

		// Update session in state
		sessionInfo := types.SessionInfo{
			ID:           props.Info.ID,
			Title:        props.Info.Title,
			CreatedAt:    parseServerTime(props.Info.Time.Created),
			UpdatedAt:    parseServerTime(props.Info.Time.Updated),
			MessageCount: 0, // Will be updated by message events
			IsActive:     true,
		}

		if err := orch.syncManager.UpdateSession(sessionInfo.ID, sessionInfo.Title, sessionInfo.IsActive, "sse"); err != nil {
			log.Printf("[SSE] Failed to update session in state: %v", err)
			return
		}
		log.Printf("[SSE] Session updated in state: %s", sessionInfo.ID)

	case "message.updated":
		// properties: { info: Message }
		type messageUpdatedProps struct {
			Info opencode.Message `json:"info"`
		}
		var props messageUpdatedProps
		if err := json.Unmarshal(env.Properties, &props); err != nil {
			log.Printf("[SSE] Failed to decode message.updated properties: %v", err)
			return
		}

		// Map to local MessageInfo and add to state
		msgType := string(props.Info.Role)
		message := types.MessageInfo{
			ID:        props.Info.ID,
			SessionID: props.Info.SessionID,
			Type:      msgType,
			Content:   "",
			Timestamp: time.Now(),
			Status:    "pending",
		}

		if err := orch.syncManager.AddMessage(message, "sse"); err != nil {
			log.Printf("[SSE] Failed to add message to state: %v", err)
			return
		}
		log.Printf("[SSE] Message added to state: %s", message.ID)

	case "message.part.updated":
		// properties: { part: { messageID, text, type, ... } }
		type partProps struct {
			Part struct {
				MessageID string `json:"messageID"`
				Text      string `json:"text"`
				Type      string `json:"type"`
			} `json:"part"`
		}
		var props partProps
		if err := json.Unmarshal(env.Properties, &props); err != nil {
			log.Printf("[SSE] Failed to decode message.part.updated properties: %v", err)
			return
		}

		// Update message content; parts aggregation not required for panel
		if err := orch.syncManager.UpdateMessage(props.Part.MessageID, props.Part.Text, "", "sse"); err != nil {
			log.Printf("[SSE] Failed to update message content: %v", err)
			return
		}
		log.Printf("[SSE] Message content updated: %s (%d chars)", props.Part.MessageID, len(props.Part.Text))

	case "message.removed":
		// properties: { messageID, sessionID }
		type messageRemovedProps struct {
			MessageID string `json:"messageID"`
			SessionID string `json:"sessionID"`
		}
		var props messageRemovedProps
		if err := json.Unmarshal(env.Properties, &props); err != nil {
			log.Printf("[SSE] Failed to decode message.removed properties: %v", err)
			return
		}

		// Construct a state update for deletion and apply via manager
		update := types.StateUpdate{
			ID:              time.Now().Format("20060102150405.000000"),
			Type:            types.MessageDeleted,
			ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
			Payload:         types.MessageDeletePayload{MessageID: props.MessageID},
			SourcePanel:     "sse",
			Timestamp:       time.Now(),
		}
		if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
			log.Printf("[SSE] Failed to delete message in state: %v", err)
			return
		}
		log.Printf("[SSE] Message deleted from state: %s", props.MessageID)

	default:
		// For other event types, just log for now
		log.Printf("[SSE] Unhandled event type: %s", env.Type)
	}
}

// mergeStreamingText merges an incoming streaming chunk with the current text,
// avoiding duplicated content when updates send the full text each time.
// Strategy:
//   - If the new chunk starts with the current text, use the new chunk (replacement).
//   - Else, find the longest overlap where the end of current matches the start of new,
//     and append only the non-overlapping suffix.
func mergeStreamingText(current, incoming string) string {
	if incoming == "" {
		return current
	}
	if current == "" {
		return incoming
	}
	// If server sends full text repeatedly, incoming will have current as prefix
	if strings.HasPrefix(incoming, current) {
		return incoming
	}
	// Compute maximal overlap between suffix of current and prefix of incoming
	max := len(current)
	if len(incoming) < max {
		max = len(incoming)
	}
	overlap := 0
	for i := 1; i <= max; i++ {
		if strings.HasSuffix(current, incoming[:i]) {
			overlap = i
		}
	}
	return current + incoming[overlap:]
}

// loadSessionsFromServer syncs local sessions with OpenCode server
// Only syncs sessions that already exist in local state (for session isolation in multi-orchestrator architecture)
func (orch *TmuxOrchestrator) loadSessionsFromServer() error {
	if orch.httpClient == nil {
		log.Printf("HTTP client unavailable; skipping session sync")
		return nil
	}
	log.Printf("Syncing sessions with OpenCode server...")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Get current local state to determine which sessions belong to this tmux session
	localState := orch.syncManager.GetState()
	localSessionIDs := make(map[string]bool)
	for _, s := range localState.Sessions {
		localSessionIDs[s.ID] = true
	}

	// If no local sessions exist, this is a fresh tmux session - don't load any server sessions
	if len(localSessionIDs) == 0 {
		log.Printf("No local sessions found - this tmux session will start fresh")
		return nil
	}

	log.Printf("Found %d local sessions to sync", len(localSessionIDs))

	// Get sessions from server
	sessions, err := orch.httpClient.Session.List(ctx, opencode.SessionListParams{})
	if err != nil {
		return fmt.Errorf("failed to list sessions from server: %w", err)
	}

	if sessions == nil || len(*sessions) == 0 {
		log.Printf("No sessions found on server")
		return nil
	}

	log.Printf("Found %d sessions on server, filtering to local sessions only", len(*sessions))

	// Only sync sessions that exist in local state (belonging to this tmux session)
	for _, serverSession := range *sessions {
		// Skip sessions that don't belong to this tmux session
		if !localSessionIDs[serverSession.ID] {
			log.Printf("Skipping session %s (not owned by this tmux session)", serverSession.ID)
			continue
		}

		sessionInfo := state.SessionInfo{
			ID:           serverSession.ID,
			Title:        serverSession.Title,
			CreatedAt:    parseServerTime(serverSession.Time.Created),
			UpdatedAt:    parseServerTime(serverSession.Time.Updated),
			MessageCount: 0,
			IsActive:     true,
		}

		// Update the session through the sync manager
		if err := orch.syncManager.AddSession(sessionInfo, "server-sync"); err != nil {
			log.Printf("Warning: Failed to sync session %s: %v", serverSession.ID, err)
			continue
		}

		log.Printf("Synced session: %s (%s)", sessionInfo.Title, sessionInfo.ID)
	}

	// First, ensure CurrentSessionID is valid before loading messages
	// This ensures proper session isolation in multi-orchestrator architecture.
	st := orch.syncManager.GetState()
	currentSessionID := ""
	if len(st.Sessions) > 0 {
		// Prefer existing CurrentSessionID if it exists in loaded sessions
		if st.CurrentSessionID != "" {
			for _, s := range st.Sessions {
				if s.ID == st.CurrentSessionID {
					currentSessionID = st.CurrentSessionID
					log.Printf("Keeping existing session selection: %s", currentSessionID)
					break
				}
			}
		}

		// If current ID is invalid or empty, select the first session
		if currentSessionID == "" {
			currentSessionID = st.Sessions[0].ID
			log.Printf("Setting session to first available: %s", currentSessionID)

			if err := orch.syncManager.UpdateSessionSelection(currentSessionID, "server-sync"); err != nil {
				log.Printf("Warning: failed to set session selection: %v", err)
			} else {
				log.Printf("Successfully set CurrentSessionID: %s", currentSessionID)
			}
		}
	} else {
		log.Printf("No sessions loaded from server, CurrentSessionID will remain empty")
	}

	// Now load message history ONLY for the current session
	if currentSessionID != "" {
		log.Printf("Loading messages only for current session: %s", currentSessionID)
		msgs, err := orch.httpClient.Session.Messages(ctx, currentSessionID, opencode.SessionMessagesParams{})
		if err != nil {
			log.Printf("Warning: Failed to load messages for session %s: %v", currentSessionID, err)
		} else if msgs != nil && len(*msgs) > 0 {
			for _, m := range *msgs {
				var messageType string
				var contentParts []string

				switch info := m.Info.AsUnion().(type) {
				case opencode.UserMessage:
					messageType = "user"
				case opencode.AssistantMessage:
					messageType = "assistant"
					_ = info // suppress unused in switch
				default:
					messageType = "system"
				}

				for _, part := range m.Parts {
					if textPart, ok := part.AsUnion().(opencode.TextPart); ok {
						contentParts = append(contentParts, textPart.Text)
					}
				}

				mi := types.MessageInfo{
					ID:        m.Info.ID,
					SessionID: currentSessionID,
					Type:      messageType,
					Content:   strings.Join(contentParts, "\n"),
					Timestamp: time.Now(),
					Status:    "completed",
				}

				if err := orch.syncManager.AddMessage(mi, "server-sync"); err != nil {
					log.Printf("Warning: Failed to add message %s for session %s: %v", mi.ID, currentSessionID, err)
				}
			}
		} else {
			log.Printf("No messages found for session %s", currentSessionID)
		}
	}

	// Log final state for debugging
	finalState := orch.syncManager.GetState()
	log.Printf("Successfully loaded %d sessions from server", len(*sessions))
	log.Printf("[INIT] Final state after loading: CurrentSessionID=%s, Sessions=%d, Messages=%d",
		finalState.CurrentSessionID, len(finalState.Sessions), len(finalState.Messages))

	// Prompt user to create session if none exist (only in terminal mode)
	if len(finalState.Sessions) == 0 && isTerminal() {
		if err := orch.promptCreateFirstSession(); err != nil {
			log.Printf("Session creation prompt returned error: %v", err)
			// Don't fail - user can create session later in the TUI
		}
	}

	return nil
}

// promptCreateFirstSession prompts the user to create a session if none exist
func (orch *TmuxOrchestrator) promptCreateFirstSession() error {
	fmt.Println("\n📋 No sessions found.")
	fmt.Print("Would you like to create a new session now? [Y/n]: ")

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	choice := strings.ToLower(strings.TrimSpace(line))
	// Default to 'y' if user just presses Enter
	if choice == "" || choice == "y" || choice == "yes" {
		fmt.Println("Creating new session...")

		// Create session via API
		ctx, cancel := context.WithTimeout(orch.ctx, 10*time.Second)
		defer cancel()

		session, err := orch.httpClient.Session.New(ctx, opencode.SessionNewParams{})
		if err != nil {
			return fmt.Errorf("failed to create session: %w", err)
		}

		fmt.Printf("✓ Session created: %s\n\n", session.Title)
		log.Printf("Created initial session: %s (ID: %s)", session.Title, session.ID)

		// Add to state
		sessionInfo := types.SessionInfo{
			ID:           session.ID,
			Title:        session.Title,
			CreatedAt:    parseServerTime(session.Time.Created),
			UpdatedAt:    parseServerTime(session.Time.Updated),
			MessageCount: 0,
			IsActive:     true,
		}

		if err := orch.syncManager.AddSession(sessionInfo, "startup-prompt"); err != nil {
			log.Printf("Warning: Failed to add session to state: %v", err)
		}

		// Set as current session
		if err := orch.syncManager.UpdateSessionSelection(session.ID, "startup-prompt"); err != nil {
			log.Printf("Warning: Failed to set current session: %v", err)
		}

		return nil
	}

	fmt.Println("Skipping session creation. You can create one later in the TUI.")
	return nil
}

// parseServerTime safely converts a server timestamp to a time.Time object.
// It returns a zero-value time if the timestamp is not positive.
func parseServerTime(timestamp float64) time.Time {
	if timestamp <= 0 {
		return time.Time{}
	}
	// The timestamp from the server is in milliseconds, so use UnixMilli.
	return time.UnixMilli(int64(timestamp))
}

// isTerminal checks if stdin is a terminal
func isTerminal() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}

// startAPIRequestHandler starts the API request handler for TUI control
func (orch *TmuxOrchestrator) startAPIRequestHandler() {
	log.Printf("Starting API request handler...")

	for {
		select {
		case <-orch.ctx.Done():
			log.Printf("API request handler shutting down")
			return
		default:
			var req struct {
				Path string          `json:"path"`
				Body json.RawMessage `json:"body"`
			}

			ctx, cancel := context.WithTimeout(orch.ctx, 5*time.Second)
			err := orch.httpClient.Get(ctx, "/tui/control/next", nil, &req)
			cancel()

			if err != nil {
				// Log error but continue - this is expected when no requests are pending
				time.Sleep(1 * time.Second)
				continue
			}

			log.Printf("Received API request: %s", req.Path)

			// Handle the API request
			response := orch.handleAPIRequest(req.Path, req.Body)

			// Send response back to server
			ctx, cancel = context.WithTimeout(orch.ctx, 5*time.Second)
			err = orch.httpClient.Post(ctx, "/tui/control/response", response, nil)
			cancel()

			if err != nil {
				log.Printf("Failed to send API response: %v", err)
			}
		}
	}
}

// handleAPIRequest handles incoming API requests
func (orch *TmuxOrchestrator) handleAPIRequest(path string, body json.RawMessage) interface{} {
	log.Printf("Handling API request: %s", path)

	switch path {
	case "/tui/open-models":
		return orch.handleOpenModelsRequest(body)
	case "/tui/open-sessions":
		return orch.handleOpenSessionsRequest(body)
	case "/tui/open-themes":
		return orch.handleOpenThemesRequest(body)
	case "/tui/open-help":
		return orch.handleOpenHelpRequest(body)
	case "/tui/open-agents":
		return orch.handleOpenAgentsRequest(body)
	default:
		log.Printf("Unknown API request path: %s", path)
		return map[string]interface{}{
			"success": false,
			"error":   "unknown request path",
		}
	}
}

// handleOpenModelsRequest handles the /tui/open-models request
func (orch *TmuxOrchestrator) handleOpenModelsRequest(body json.RawMessage) interface{} {
	log.Printf("Handling open models request")

	// Create a state update to trigger model dialog opening in all connected panels
	update := types.StateUpdate{
		ID:              fmt.Sprintf("open_models_%d", time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: "open_models",
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	// Apply the update through sync manager
	if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
		log.Printf("Failed to trigger open models action: %v", err)
		return map[string]interface{}{
			"success": false,
			"error":   "failed to trigger model dialog",
		}
	}

	log.Printf("Successfully triggered open models action")
	return true
}

// handleOpenAgentsRequest handles the /tui/open-agents request
func (orch *TmuxOrchestrator) handleOpenAgentsRequest(body json.RawMessage) interface{} {
	log.Printf("Handling open agents request")

	// Create a state update to trigger agent dialog opening
	update := types.StateUpdate{
		ID:              fmt.Sprintf("open_agents_%d", time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: "open_agents",
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
		log.Printf("Failed to trigger open agents action: %v", err)
		return map[string]interface{}{
			"success": false,
			"error":   "failed to trigger agent dialog",
		}
	}

	return true
}

// handleOpenSessionsRequest handles the /tui/open-sessions request
func (orch *TmuxOrchestrator) handleOpenSessionsRequest(body json.RawMessage) interface{} {
	log.Printf("Handling open sessions request")

	// Create a state update to trigger session dialog opening
	update := types.StateUpdate{
		ID:              fmt.Sprintf("open_sessions_%d", time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: "open_sessions",
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
		log.Printf("Failed to trigger open sessions action: %v", err)
		return map[string]interface{}{
			"success": false,
			"error":   "failed to trigger session dialog",
		}
	}

	return true
}

// handleOpenThemesRequest handles the /tui/open-themes request
func (orch *TmuxOrchestrator) handleOpenThemesRequest(body json.RawMessage) interface{} {
	log.Printf("Handling open themes request")

	// Create a state update to trigger theme dialog opening
	update := types.StateUpdate{
		ID:              fmt.Sprintf("open_themes_%d", time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: "open_themes",
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
		log.Printf("Failed to trigger open themes action: %v", err)
		return map[string]interface{}{
			"success": false,
			"error":   "failed to trigger theme dialog",
		}
	}

	return true
}

// handleOpenHelpRequest handles the /tui/open-help request
func (orch *TmuxOrchestrator) handleOpenHelpRequest(body json.RawMessage) interface{} {
	log.Printf("Handling open help request")

	// Create a state update to trigger help dialog opening
	update := types.StateUpdate{
		ID:              fmt.Sprintf("open_help_%d", time.Now().UnixNano()),
		Type:            types.UIActionTriggered,
		ExpectedVersion: orch.syncManager.GetState().GetCurrentVersion(),
		Payload: types.UIActionPayload{
			Action: "open_help",
		},
		SourcePanel: "tmux-orchestrator",
		Timestamp:   time.Now(),
	}

	if err := orch.syncManager.UpdateWithVersionCheck(update); err != nil {
		log.Printf("Failed to trigger open help action: %v", err)
		return map[string]interface{}{
			"success": false,
			"error":   "failed to trigger help dialog",
		}
	}

	return true
}
