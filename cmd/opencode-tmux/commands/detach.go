package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
)

// CmdDetach implements the 'detach' subcommand
func CmdDetach(args []string) error {
	fs := flag.NewFlagSet("detach", flag.ExitOnError)
	allClients := fs.Bool("all", false, "Detach all clients from the session")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux detach [options] [session-name]\n\n")
		fmt.Fprintf(os.Stderr, "Explicitly disconnect from the current tmux session (daemon continues running).\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	sessionName := getSessionName(fs.Args())

	// If --all specified, detach all clients
	if *allClients {
		if !sessionExists(sessionName) {
			return fmt.Errorf("session '%s' does not exist", sessionName)
		}

		log.Printf("Detaching all clients from session '%s'", sessionName)
		cmd := exec.Command("tmux", "detach-client", "-s", sessionName, "-a")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}

	// Normal detach - must be inside tmux session
	if !inTmuxSession() {
		return fmt.Errorf("not in a tmux session\nUse 'tmux detach-client' from within a tmux session, or 'opencode-tmux detach --all %s' to detach all clients", sessionName)
	}

	currentSession := getCurrentTmuxSession()
	log.Printf("Detaching from session '%s'", currentSession)

	cmd := exec.Command("tmux", "detach-client")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
