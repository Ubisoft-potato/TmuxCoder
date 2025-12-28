package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencode/tmux_coder/internal/ipc"
)

// RunLegacyWithArgs is a bridge function that will be set by main.go
// to call the legacy runLegacyMode with modified arguments
var RunLegacyWithArgs func(args []string) error

// StartOptions holds options for the start command
type StartOptions struct {
	// Session configuration
	SessionName string
	ConfigPath  string
	LayoutPath  string

	// Server configuration
	ServerURL string
	APIKey    string

	// Behavior flags
	Detach        bool // Start daemon without attaching (renamed from ServerOnly)
	Daemon        bool // Run in daemon mode (ignore Ctrl+C)
	ReuseExisting bool // [DEPRECATED] Reuse existing tmux session (now automatic)
	ForceNew      bool // Force kill existing and create new
	AttachRead    bool // Attach in read-only mode
	AttachOnly    bool // Only attach, don't configure

	// Merge target
	MergeInto string // tmux session name to merge into

	// Advanced options
	NoAutoStart  bool   // Don't start panels automatically
	DetachKeys   string // Custom detach key sequence
	ReloadLayout bool   // Reload layout without restarting

	// Prompt configuration
	CustomSP          string // Custom system prompt (on/off/auto)
	CleanDefaultEnvSP string // Clean default environment system prompt (on/off/auto)
}

// CmdStart implements the 'start' subcommand
func CmdStart(args []string) error {
	opts := &StartOptions{}

	// Parse flags
	fs := flag.NewFlagSet("start", flag.ExitOnError)

	// Session flags
	fs.StringVar(&opts.ConfigPath, "config", "", "Path to configuration file")
	fs.StringVar(&opts.LayoutPath, "layout", "", "Path to layout file")

	// Server flags
	fs.StringVar(&opts.ServerURL, "server", os.Getenv("OPENCODE_SERVER"), "OpenCode server URL")
	fs.StringVar(&opts.APIKey, "api-key", os.Getenv("OPENCODE_API_KEY"), "OpenCode API key")

	// Behavior flags
	fs.BoolVar(&opts.Detach, "detach", false, "Start daemon without attaching")
	fs.BoolVar(&opts.Detach, "server-only", false, "Start daemon without attaching (alias for --detach)")
	fs.BoolVar(&opts.Daemon, "daemon", false, "Run in daemon mode (ignore Ctrl+C)")
	fs.BoolVar(&opts.ReuseExisting, "reuse", false, "Reuse existing tmux session")
	fs.BoolVar(&opts.ReuseExisting, "reuse-session", false, "Reuse existing tmux session (alias)")
	fs.BoolVar(&opts.ForceNew, "force", false, "Force kill existing and create new")
	fs.BoolVar(&opts.ForceNew, "force-new", false, "Force kill existing and create new (alias)")
	fs.BoolVar(&opts.ForceNew, "force-new-session", false, "Force kill existing and create new (alias)")
	fs.BoolVar(&opts.AttachRead, "read-only", false, "Attach in read-only mode")
	fs.BoolVar(&opts.AttachOnly, "attach-only", false, "Only attach to existing session")

	// Merge target
	fs.StringVar(&opts.MergeInto, "merge-into", "", "Merge into an existing tmux session (create a new window there)")

	// Advanced flags
	fs.BoolVar(&opts.NoAutoStart, "no-auto-start", false, "Don't start panels automatically")
	fs.StringVar(&opts.DetachKeys, "detach-keys", "", "Custom tmux detach key sequence")
	fs.BoolVar(&opts.ReloadLayout, "reload-layout", false, "Reload layout without restarting")

	// Prompt configuration flags
	fs.StringVar(&opts.CustomSP, "custom-sp", os.Getenv("TMUXCODER_CUSTOM_SP"), "Enable custom system prompt (on/off/auto)")
	fs.StringVar(&opts.CleanDefaultEnvSP, "clean-default-env-sp", os.Getenv("TMUXCODER_CLEAN_DEFAULT_ENV_SP"), "Clean default environment system prompt (on/off/auto)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux start [session-name] [options]\n\n")
		fmt.Fprintf(os.Stderr, "Start orchestrator daemon and tmux session.\n\n")
		fmt.Fprintf(os.Stderr, "Behavior:\n")
		fmt.Fprintf(os.Stderr, "  - If session exists: automatically reuses it (smart reuse)\n")
		fmt.Fprintf(os.Stderr, "  - If daemon crashed: automatically recovers and reuses tmux session\n")
		fmt.Fprintf(os.Stderr, "  - Use --force to override and recreate from scratch\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  # Normal start (creates or reuses automatically)\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux start mysession\n\n")
		fmt.Fprintf(os.Stderr, "  # Start daemon in background (two-step workflow)\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux start mysession --daemon --detach\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux attach mysession\n\n")
		fmt.Fprintf(os.Stderr, "  # Force recreate session\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux start mysession --force\n")
	}

	// Reorder args: flags first, then positional
	// This is needed because Go's flag package stops parsing after the first non-flag
	reorderedArgs := reorderArgs(args)

	if err := fs.Parse(reorderedArgs); err != nil {
		return err
	}

	// Get session name
	opts.SessionName = getSessionName(fs.Args())

	// Validate flags
	if err := opts.Validate(); err != nil {
		return err
	}

	// Check for conflicting flags
	if opts.ReuseExisting && opts.ForceNew {
		return fmt.Errorf("--reuse and --force are mutually exclusive")
	}
	if opts.AttachOnly && opts.ForceNew {
		return fmt.Errorf("cannot combine --attach-only with --force")
	}
	if opts.AttachOnly && opts.Detach {
		return fmt.Errorf("cannot combine --attach-only with --detach")
	}
	if opts.ReloadLayout && opts.Detach {
		return fmt.Errorf("cannot combine --reload-layout with --detach")
	}
	if opts.ReloadLayout && opts.ForceNew {
		return fmt.Errorf("cannot combine --reload-layout with --force")
	}
	if opts.ReloadLayout && opts.AttachOnly {
		return fmt.Errorf("cannot combine --reload-layout with --attach-only")
	}

	if strings.TrimSpace(opts.MergeInto) != "" && opts.AttachOnly {
		return fmt.Errorf("cannot combine --merge-into with --attach-only")
	}
	if strings.TrimSpace(opts.MergeInto) != "" && opts.ForceNew {
		return fmt.Errorf("cannot combine --merge-into with --force")
	}

	// Handle reload-layout command (special case)
	if opts.ReloadLayout {
		return handleReloadLayout(opts)
	}

	// Apply prompt configuration environment variables
	if err := applyPromptConfig(opts); err != nil {
		return err
	}

	// Execute start
	return executeStart(opts)
}

// Validate checks if options are valid
func (opts *StartOptions) Validate() error {
	if opts.ServerURL == "" && !opts.Detach && !opts.AttachOnly {
		return fmt.Errorf("server URL is required (set OPENCODE_SERVER or use --server)")
	}
	return nil
}

// handleReloadLayout sends a reload layout IPC command
func handleReloadLayout(opts *StartOptions) error {
	socketPath := getSocketPath(opts.SessionName)

	// Check if daemon is running
	if !isSocketActive(socketPath) {
		return fmt.Errorf("daemon not running for session '%s'", opts.SessionName)
	}

	// Prepare override path (if any)
	overridePath := resolveReloadConfigPath(opts)

	// Send reload command via IPC
	client := ipc.NewSocketClient(socketPath, fmt.Sprintf("cli-reload-%d", os.Getpid()), "controller")
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Disconnect()

	params := map[string]interface{}{}
	if overridePath != "" {
		params["config_path"] = overridePath
	}

	if err := client.SendOrchestratorCommandWithParams("reload_layout", params); err != nil {
		return fmt.Errorf("failed to send reload command: %w", err)
	}

	fmt.Println("Reload layout command sent successfully.")
	return nil
}

func resolveReloadConfigPath(opts *StartOptions) string {
	path := ""
	if opts.LayoutPath != "" {
		path = opts.LayoutPath
	} else if opts.ConfigPath != "" {
		path = opts.ConfigPath
	} else if env := os.Getenv("OPENCODE_TMUX_CONFIG"); env != "" {
		path = env
	}

	if path == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		path = filepath.Join(homeDir, ".opencode", "tmux.yaml")
	}

	if strings.HasPrefix(path, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			path = filepath.Join(homeDir, path[2:])
		}
	}

	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

// applyPromptConfig sets environment variables for prompt configuration
func applyPromptConfig(opts *StartOptions) error {
	if err := applyToggleEnv("TMUXCODER_CUSTOM_SP", opts.CustomSP, "Custom SP"); err != nil {
		return err
	}
	if err := applyToggleEnv("TMUXCODER_CLEAN_DEFAULT_ENV_SP", opts.CleanDefaultEnvSP, "Clean default env SP"); err != nil {
		return err
	}
	return nil
}

// applyToggleEnv sets or unsets an environment variable based on toggle value
func applyToggleEnv(envName, value, label string) error {
	if value == "" {
		return nil
	}

	// Normalize value
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "on", "true", "1", "enable", "enabled", "yes":
		if err := os.Setenv(envName, "on"); err != nil {
			return fmt.Errorf("failed to set %s: %w", envName, err)
		}
		fmt.Printf("%s forced on\n", label)
	case "off", "false", "0", "disable", "disabled", "no":
		if err := os.Setenv(envName, "off"); err != nil {
			return fmt.Errorf("failed to set %s: %w", envName, err)
		}
		fmt.Printf("%s forced off\n", label)
	case "auto":
		if err := os.Unsetenv(envName); err != nil {
			return fmt.Errorf("failed to unset %s: %w", envName, err)
		}
		fmt.Printf("%s override cleared (auto)\n", label)
	default:
		if normalized != "" {
			return fmt.Errorf("invalid toggle value %q for %s (expected on/off/auto)", value, label)
		}
	}
	return nil
}

// executeStart performs the actual start operation
func executeStart(opts *StartOptions) error {
	// Setup logging
	if err := setupStartLogging(opts); err != nil {
		log.Printf("Warning: failed to setup logging: %v", err)
	}

	log.Printf("Starting session '%s' with options: daemon=%v, reuse=%v, force=%v",
		opts.SessionName, opts.Daemon, opts.ReuseExisting, opts.ForceNew)

	// Check if session already exists
	status := checkSessionStatus(opts.SessionName)

	// Handle different scenarios
	if err := handleExistingSession(opts, status); err != nil {
		return err
	}

	// Build legacy-compatible arguments and delegate to runLegacyMode
	// This is a transitional bridge until full orchestrator integration is complete
	args := buildLegacyArgs(opts)

	// Call the legacy runner via exported bridge function
	return RunLegacyWithArgs(args)
}

// setupStartLogging configures logging for start command
func setupStartLogging(opts *StartOptions) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	logDir := filepath.Join(homeDir, ".opencode")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}

	logPath := filepath.Join(logDir, fmt.Sprintf("tmux-%s.log", opts.SessionName))
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	log.SetOutput(logFile)
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	return nil
}

// handleExistingSession handles scenarios where session already exists
// Now with intelligent automatic reuse behavior
func handleExistingSession(opts *StartOptions, status SessionStatus) error {
	// Session doesn't exist - nothing to handle
	if !status.TmuxRunning && !status.DaemonRunning {
		return nil
	}

	// Both running - auto reuse unless --force
	if status.TmuxRunning && status.DaemonRunning {
		if opts.ForceNew {
			log.Printf("Force flag set, will kill existing session")
			fmt.Printf("Forcing restart of session '%s'\n", opts.SessionName)
			// Let legacy mode handle the force cleanup
			return nil
		}

		// Auto reuse: session is running normally
		log.Printf("Session '%s' already running, reusing automatically", opts.SessionName)
		fmt.Printf("Session '%s' is already running\n", opts.SessionName)
		fmt.Printf("Daemon: Running | Clients: %d\n", status.ClientCount)
		fmt.Printf("Reusing existing session (use --force to restart)\n")

		// Enable reuse flag for legacy mode
		opts.ReuseExisting = true
		return nil
	}

	// Orphaned session (tmux running but no daemon) - auto recover
	if status.TmuxRunning && !status.DaemonRunning {
		if opts.ForceNew {
			log.Printf("Force flag set, will recreate orphaned session")
			fmt.Printf("Recreating orphaned session '%s'\n", opts.SessionName)
			return nil
		}

		// Auto recover: restart daemon and reuse tmux session
		log.Printf("Found orphaned session '%s' (daemon not running)", opts.SessionName)
		fmt.Printf("Found orphaned session '%s' (daemon crashed or stopped)\n", opts.SessionName)
		fmt.Printf("Automatically recovering: restarting daemon and reusing tmux session\n")

		// Enable reuse flag for legacy mode
		opts.ReuseExisting = true
		return nil
	}

	// Daemon running but no tmux (inconsistent state)
	if !status.TmuxRunning && status.DaemonRunning {
		if opts.ForceNew {
			log.Printf("Force flag set, will clean up inconsistent state")
			return nil
		}

		fmt.Fprintf(os.Stderr, "Warning: Inconsistent state detected\n")
		fmt.Fprintf(os.Stderr, "Daemon is running but tmux session doesn't exist\n")
		fmt.Fprintf(os.Stderr, "Use --force to clean up and restart\n")
		return fmt.Errorf("inconsistent session state")
	}

	return nil
}

// reorderArgs reorders arguments so flags come before positional arguments
// This is necessary because Go's flag package stops parsing after the first non-flag argument
func reorderArgs(args []string) []string {
	flags := []string{}
	positionals := []string{}
	expectValue := false // Track if next arg is a flag value

	for i, arg := range args {
		if expectValue {
			// This is a value for the previous flag
			flags = append(flags, arg)
			expectValue = false
		} else if strings.HasPrefix(arg, "-") {
			// This is a flag
			flags = append(flags, arg)

			// Check if this flag expects a value (not a boolean flag)
			// Boolean flags in our command: --daemon, --detach, --server-only, --reuse,
			// --force, --read-only, --attach-only, --no-auto-start, --reload-layout
			isBoolFlag := arg == "--daemon" || arg == "--detach" || arg == "--server-only" ||
				arg == "--reuse" || arg == "--reuse-session" || arg == "--force" ||
				arg == "--force-new" || arg == "--force-new-session" ||
				arg == "--read-only" || arg == "--attach-only" ||
				arg == "--no-auto-start" || arg == "--reload-layout"

			// Check if flag has =value format
			hasEquals := strings.Contains(arg, "=")

			// If not a bool flag and not using = format, next arg is the value
			if !isBoolFlag && !hasEquals && i+1 < len(args) {
				expectValue = true
			}
		} else {
			// This is a positional argument (session name)
			positionals = append(positionals, arg)
		}
	}

	// Return flags first, then positionals
	return append(flags, positionals...)
}

// buildLegacyArgs converts StartOptions to legacy command-line arguments
func buildLegacyArgs(opts *StartOptions) []string {
	args := []string{}

	// Add flags
	if opts.Detach {
		args = append(args, "--server-only")
	}
	if opts.Daemon {
		args = append(args, "--daemon")
	}
	if opts.ReuseExisting {
		args = append(args, "--reuse-session")
	}
	if opts.ForceNew {
		args = append(args, "--force-new-session")
	}
	if opts.AttachOnly {
		args = append(args, "--attach-only")
	}
	if opts.ReloadLayout {
		args = append(args, "--reload-layout")
	}
	if strings.TrimSpace(opts.MergeInto) != "" {
		// Use = form so the merge target isn't mistaken for a positional session name
		// by legacy startup code that scans for the first non-flag argument.
		args = append(args, "--merge-into="+opts.MergeInto)
	}

	// Add session name as positional argument
	args = append(args, opts.SessionName)

	return args
}
