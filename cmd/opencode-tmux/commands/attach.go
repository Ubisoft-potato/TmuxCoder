package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	friendlyerrors "github.com/opencode/tmux_coder/internal/errors"
)

// CmdAttach implements the 'attach' subcommand
func CmdAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	readOnly := fs.Bool("read-only", false, "Attach in read-only mode")
	autoStart := fs.Bool("auto-start", false, "Automatically start daemon if not running")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux attach [options] [session-name]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to an existing tmux session.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux attach mysession\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux attach mysession --auto-start\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux attach mysession --read-only\n")
	}

	// Reorder args: flags first, then positional
	// This is needed because Go's flag package stops parsing after the first non-flag
	reorderedArgs := reorderAttachArgs(args)

	if err := fs.Parse(reorderedArgs); err != nil {
		return err
	}

	sessionName := getSessionName(fs.Args())

	// Check if session exists
	if !sessionExists(sessionName) {
		// If auto-start is enabled, create the session
		if *autoStart {
			fmt.Printf("Session '%s' does not exist, creating it with --auto-start\n", sessionName)
			// Delegate to start command
			return CmdStart([]string{sessionName, "--daemon"})
		}
		// Return user-friendly error with hints
		return friendlyerrors.SessionNotFound(sessionName)
	}

	// Check if daemon is running
	daemonRunning := isDaemonRunning(sessionName)
	if !daemonRunning {
		if *autoStart {
			fmt.Printf("Daemon not running for session '%s', starting it with --auto-start\n", sessionName)
			log.Printf("Auto-starting daemon for orphaned session '%s'", sessionName)

			// Start the daemon without attaching first
			if err := CmdStart([]string{sessionName, "--daemon", "--detach"}); err != nil {
				return fmt.Errorf("failed to auto-start daemon: %w", err)
			}

			// Small delay to let daemon initialize
			fmt.Printf("Daemon started, attaching to session...\n")
		} else {
			log.Printf("Warning: Orchestrator daemon is not running for session '%s'", sessionName)
			log.Printf("Session exists but panels are not supervised")
			fmt.Fprintf(os.Stderr, "Warning: Daemon not running (use --auto-start to start it automatically)\n")
			fmt.Fprintf(os.Stderr, "Or manually start: opencode-tmux start %s --daemon --detach\n\n", sessionName)
		}
	}

	// Attach to session
	log.Printf("Attaching to session '%s'", sessionName)

	tmuxArgs := []string{"attach-session", "-t", sessionName}
	if *readOnly {
		tmuxArgs = append(tmuxArgs, "-r")
	}

	cmd := exec.Command("tmux", tmuxArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		// Wrap with user-friendly error
		return friendlyerrors.AttachFailed(sessionName, err)
	}

	return nil
}

// reorderAttachArgs reorders arguments so flags come before positional arguments
// This is necessary because Go's flag package stops parsing after the first non-flag argument
func reorderAttachArgs(args []string) []string {
	flags := []string{}
	positionals := []string{}

	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
		} else {
			positionals = append(positionals, arg)
		}
	}

	// Return flags first, then positionals
	return append(flags, positionals...)
}
