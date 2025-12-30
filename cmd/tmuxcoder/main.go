package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/opencode/tmux_coder/cmd/tmuxcoder/internal"
)

const version = "2.0.0"

func main() {
	remainingArgs, opts, err := parseGlobalOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := applyGlobalOptions(opts); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	app := internal.NewApp()
	defer app.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "\nReceived %s, shutting down...\n", sig)
		app.Close()
		os.Exit(1)
	}()

	// Handle zero-argument case: smart start with merge prompt
	if len(remainingArgs) == 0 {
		if err := app.SmartStart(""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Parse command
	cmd := remainingArgs[0]
	args := []string{}
	if len(remainingArgs) > 1 {
		args = remainingArgs[1:]
	}

	// Handle subcommands
	switch cmd {
	case "help", "-h", "--help":
		printHelp()

	case "version", "-v", "--version":
		fmt.Printf("tmuxcoder v%s\n", version)

	case "list", "ls":
		if err := app.ListSessions(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "new", "start":
		if err := app.CreateSession(args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "attach", "a":
		if err := app.AttachSession(args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "stop", "kill":
		if err := app.StopSession(args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "status", "st":
		if err := app.ShowStatus(args); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "layout":
		if len(args) < 1 {
			fmt.Fprintf(os.Stderr, "Error: layout command expects: tmuxcoder layout <session> [layout.yaml]\n")
			os.Exit(1)
		}
		sessionName := args[0]
		layoutPath := ""
		if len(args) > 1 {
			layoutPath = args[1]
		}
		if err := app.ReloadLayout(sessionName, layoutPath); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	default:
		// If first arg looks like a session name (no dashes), treat as smart start
		if cmd[0] != '-' {
			if err := app.SmartStart(cmd); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			// Pass through to opencode-tmux for legacy flags
			if err := app.PassThrough(remainingArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func printHelp() {
	help := `tmuxcoder - Zero-config AI coding orchestrator

USAGE:
    tmuxcoder [GLOBAL OPTIONS] [COMMAND] [OPTIONS]

GLOBAL OPTIONS:
    --layout <path>              Override layout config (sets OPENCODE_TMUX_CONFIG)
    --custom-sp <mode>           Force custom system prompt (on/off/auto). Use --no-custom-sp as shorthand for off.
    --clean-default-env-sp <mode> Clean default environment system prompt (on/off/auto). Use --no-clean-default-env-sp for off.
    --force-restart-server       Force restart OpenCode server if prompt config changes (default: reuse existing server)

COMMANDS:
    (no command)           Smart start - auto-detect session and attach
    <session-name>         Start or attach to named session
    attach <name>          Attach to existing session (alias: a)
    layout <name> [file]   Reload layout for session without attaching
    stop <name> [--cleanup|-c]
                           Stop session daemon (alias: kill)
                           --cleanup: Also kill tmux session
    list                   List all sessions (alias: ls)
    status [name]          Show session status (alias: st)
    help                   Show this help
    version                Show version

EXAMPLES:
    # Zero-config startup (auto-detects session name from directory)
    tmuxcoder

    # Start/attach to specific session
    tmuxcoder myproject

    # List all sessions
    tmuxcoder list

    # Stop daemon only (tmux session remains)
    tmuxcoder stop myproject

    # Stop daemon and destroy tmux session
    tmuxcoder stop myproject --cleanup

    # Show status
    tmuxcoder status

BEHAVIOR:
    - Automatically creates tmux session if it doesn't exist
    - Automatically starts daemon in background
    - Automatically attaches to session
    - Auto-detects session name from current directory
    - Reuses existing sessions intelligently

ENVIRONMENT VARIABLES:
    TMUXCODER_ROOT                   Project root directory
    OPENCODE_SERVER                  OpenCode API server URL
    OPENCODE_TMUX_CONFIG             Config file (default: ~/.opencode/tmux.yaml)
    TMUXCODER_CUSTOM_SP              Force custom system prompt (on/off)
    TMUXCODER_CLEAN_DEFAULT_ENV_SP   Clean default environment system prompt (on/off)

`
	fmt.Print(help)
}

type globalOptions struct {
	layoutPath          string
	serverURL           string
	customSP            string
	cleanDefaultEnvSP   string
	forceRestartServer  bool
}

func parseGlobalOptions(args []string) ([]string, globalOptions, error) {
	opts := globalOptions{}
	remaining := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			remaining = append(remaining, args[i:]...)
			break
		}

		if arg == "--layout" || strings.HasPrefix(arg, "--layout=") {
			value := ""
			if arg == "--layout" {
				if i+1 >= len(args) {
					return nil, opts, fmt.Errorf("--layout requires a file path")
				}
				value = args[i+1]
				i++
			} else {
				value = strings.TrimPrefix(arg, "--layout=")
			}

			if value == "" {
				return nil, opts, fmt.Errorf("--layout requires a file path")
			}

			opts.layoutPath = value
			continue
		}

		if arg == "--server" || strings.HasPrefix(arg, "--server=") {
			value := ""
			if arg == "--server" {
				if i+1 >= len(args) {
					return nil, opts, fmt.Errorf("--server requires a URL")
				}
				value = args[i+1]
				i++
			} else {
				value = strings.TrimPrefix(arg, "--server=")
			}

			if value == "" {
				return nil, opts, fmt.Errorf("--server requires a URL")
			}

			opts.serverURL = value
			continue
		}

		if arg == "--custom-sp" || strings.HasPrefix(arg, "--custom-sp=") {
			value, err := getFlagValue(args, &i, arg, "--custom-sp")
			if err != nil {
				return nil, opts, err
			}
			normalized, err := normalizeToggleValue(value)
			if err != nil {
				return nil, opts, err
			}
			opts.customSP = normalized
			continue
		}

		if arg == "--no-custom-sp" {
			opts.customSP = "off"
			continue
		}

		if arg == "--clean-default-env-sp" || strings.HasPrefix(arg, "--clean-default-env-sp=") {
			value, err := getFlagValue(args, &i, arg, "--clean-default-env-sp")
			if err != nil {
				return nil, opts, err
			}
			normalized, err := normalizeToggleValue(value)
			if err != nil {
				return nil, opts, err
			}
			opts.cleanDefaultEnvSP = normalized
			continue
		}

		if arg == "--no-clean-default-env-sp" {
			opts.cleanDefaultEnvSP = "off"
			continue
		}

		if arg == "--force-restart-server" {
			opts.forceRestartServer = true
			continue
		}

		remaining = append(remaining, arg)
	}

	return remaining, opts, nil
}

func getFlagValue(args []string, index *int, arg, flag string) (string, error) {
	if arg == flag {
		if *index+1 >= len(args) {
			return "", fmt.Errorf("%s requires a value", flag)
		}
		value := args[*index+1]
		*index++
		return value, nil
	}
	value := strings.TrimPrefix(arg, flag+"=")
	if value == "" {
		return "", fmt.Errorf("%s requires a value", flag)
	}
	return value, nil
}

func normalizeToggleValue(raw string) (string, error) {
	value := strings.ToLower(strings.TrimSpace(raw))
	switch value {
	case "on", "true", "1", "enable", "enabled", "yes":
		return "on", nil
	case "off", "false", "0", "disable", "disabled", "no":
		return "off", nil
	case "auto":
		return "auto", nil
	default:
		if value == "" {
			return "", fmt.Errorf("toggle value cannot be empty (expected on/off/auto)")
		}
		return "", fmt.Errorf("invalid toggle value %q (expected on/off/auto)", raw)
	}
}

func applyGlobalOptions(opts globalOptions) error {
	if opts.layoutPath == "" {
		_ = os.Unsetenv("TMUXCODER_LAYOUT_OVERRIDE_PATH")
	} else {
		resolved, err := resolvePath(opts.layoutPath)
		if err != nil {
			return fmt.Errorf("invalid layout path: %w", err)
		}

		if _, err := os.Stat(resolved); err != nil {
			return fmt.Errorf("layout file not accessible: %w", err)
		}

		if err := os.Setenv("OPENCODE_TMUX_CONFIG", resolved); err != nil {
			return fmt.Errorf("failed to set OPENCODE_TMUX_CONFIG: %w", err)
		}
		if err := os.Setenv("TMUXCODER_LAYOUT_OVERRIDE_PATH", resolved); err != nil {
			return fmt.Errorf("failed to propagate layout override: %w", err)
		}

		fmt.Printf("Using layout config: %s\n", resolved)
	}

	if opts.serverURL != "" {
		if err := os.Setenv("OPENCODE_SERVER", opts.serverURL); err != nil {
			return fmt.Errorf("failed to set OPENCODE_SERVER: %w", err)
		}
		fmt.Printf("Using OpenCode server: %s\n", opts.serverURL)
	}

	if err := applyToggleOverride("TMUXCODER_CUSTOM_SP", opts.customSP, "Custom SP"); err != nil {
		return err
	}

	if err := applyToggleOverride("TMUXCODER_CLEAN_DEFAULT_ENV_SP", opts.cleanDefaultEnvSP, "Clean default env SP"); err != nil {
		return err
	}

	if opts.forceRestartServer {
		if err := os.Setenv("TMUXCODER_FORCE_RESTART_SERVER", "true"); err != nil {
			return fmt.Errorf("failed to set TMUXCODER_FORCE_RESTART_SERVER: %w", err)
		}
		fmt.Println("Force restart server enabled")
	}

	return nil
}

func applyToggleOverride(envName, value, label string) error {
	if value == "" {
		return nil
	}

	if value == "auto" {
		if err := os.Unsetenv(envName); err != nil {
			return fmt.Errorf("failed to unset %s: %w", envName, err)
		}
		fmt.Printf("%s override cleared (auto)\n", label)
		return nil
	}

	if err := os.Setenv(envName, value); err != nil {
		return fmt.Errorf("failed to set %s: %w", envName, err)
	}
	fmt.Printf("%s forced %s\n", label, value)
	return nil
}

func resolvePath(path string) (string, error) {
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
