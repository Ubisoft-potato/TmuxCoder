package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
)

// CmdAttach implements the 'attach' subcommand
func CmdAttach(args []string) error {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	readOnly := fs.Bool("read-only", false, "Attach in read-only mode")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux attach [options] [session-name]\n\n")
		fmt.Fprintf(os.Stderr, "Connect to an existing tmux session.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	sessionName := getSessionName(fs.Args())

	// Check if session exists
	if !sessionExists(sessionName) {
		return fmt.Errorf("session '%s' does not exist\nAvailable commands:\n  - Create new session: opencode-tmux start %s\n  - List sessions: opencode-tmux list", sessionName, sessionName)
	}

	// Check if daemon is running
	daemonRunning := isDaemonRunning(sessionName)
	if !daemonRunning {
		log.Printf("Warning: Orchestrator daemon is not running for session '%s'", sessionName)
		log.Printf("Session exists but panels are not supervised")
		log.Printf("To restore management: opencode-tmux start %s --reuse", sessionName)
		fmt.Fprintf(os.Stderr, "\n")
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

	return cmd.Run()
}
