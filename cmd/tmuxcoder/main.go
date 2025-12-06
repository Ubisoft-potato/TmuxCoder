package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/opencode/tmux_coder/cmd/tmuxcoder/internal"
)

const version = "2.0.0"

func main() {
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

	// Handle zero-argument case: smart start
	if len(os.Args) == 1 {
		if err := app.SmartStart(""); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	// Parse command
	cmd := os.Args[1]
	args := []string{}
	if len(os.Args) > 2 {
		args = os.Args[2:]
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

	default:
		// If first arg looks like a session name (no dashes), treat as smart start
		if cmd[0] != '-' {
			if err := app.SmartStart(cmd); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		} else {
			// Pass through to opencode-tmux for legacy flags
			if err := app.PassThrough(os.Args[1:]); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}
	}
}

func printHelp() {
	help := `tmuxcoder - Zero-config AI coding orchestrator

USAGE:
    tmuxcoder [COMMAND] [OPTIONS]

COMMANDS:
    (no command)           Smart start - auto-detect session and attach
    <session-name>         Start or attach to named session
    new <name>             Create new session (alias: start)
    attach <name>          Attach to existing session (alias: a)
    stop <name>            Stop session daemon (alias: kill)
    list                   List all sessions (alias: ls)
    status [name]          Show session status (alias: st)
    help                   Show this help
    version                Show version

EXAMPLES:
    # Zero-config startup (auto-detects session name from directory)
    tmuxcoder

    # Start/attach to specific session
    tmuxcoder myproject

    # Create new session
    tmuxcoder new backend-dev

    # List all sessions
    tmuxcoder list

    # Stop a session
    tmuxcoder stop myproject

    # Show status
    tmuxcoder status

BEHAVIOR:
    - Automatically creates tmux session if it doesn't exist
    - Automatically starts daemon in background
    - Automatically attaches to session
    - Auto-detects session name from current directory
    - Reuses existing sessions intelligently

ENVIRONMENT VARIABLES:
    TMUXCODER_ROOT            Project root directory
    OPENCODE_SERVER           OpenCode API server URL
    OPENCODE_TMUX_CONFIG      Config file (default: ~/.opencode/tmux.yaml)

NOTES:
    - This is the Phase 2 enhanced wrapper for opencode-tmux
    - Implements true one-command workflow with auto-attach
    - Legacy opencode-tmux commands still available via pass-through

For more information: https://github.com/yourusername/TmuxCoder
`
	fmt.Print(help)
}
