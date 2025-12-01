package commands

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/opencode/tmux_coder/internal/ipc"
)

// CmdStop implements the 'stop' subcommand
func CmdStop(args []string) error {
	fs := flag.NewFlagSet("stop", flag.ExitOnError)
	cleanup := fs.Bool("cleanup", false, "Also destroy tmux session and terminate all panels")
	checkClients := fs.Bool("check-clients", false, "Refuse to stop if clients are connected (unless --force)")
	force := fs.Bool("force", false, "Force stop even if clients are connected")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux stop [options] [session-name]\n\n")
		fmt.Fprintf(os.Stderr, "Stop the orchestrator daemon.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux stop mysession             # Stop daemon, preserve tmux session\n")
		fmt.Fprintf(os.Stderr, "  opencode-tmux stop mysession --cleanup   # Stop daemon and destroy session\n")
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	sessionName := getSessionName(fs.Args())

	// Check if daemon is running
	socketPath := getSocketPath(sessionName)
	if !isSocketActive(socketPath) {
		return fmt.Errorf("orchestrator daemon is not running for session '%s'\nSocket: %s", sessionName, socketPath)
	}

	// Check client connections if requested
	if *checkClients && !*force {
		clients, err := getConnectedClients(sessionName)
		if err == nil && len(clients) > 0 {
			fmt.Fprintf(os.Stderr, "Error: %d client(s) still connected to session '%s'\n", len(clients), sessionName)
			fmt.Fprintf(os.Stderr, "Use --force to stop anyway, or ask other users to detach first.\n\n")
			fmt.Fprintf(os.Stderr, "Connected clients:\n")
			for _, client := range clients {
				fmt.Fprintf(os.Stderr, "  - %s (PID %s)\n", client.TTY, client.PID)
			}
			return fmt.Errorf("clients still connected")
		}
	}

	// Send shutdown command via IPC
	log.Printf("Sending shutdown command to daemon (cleanup=%v)", *cleanup)

	// Create a helper to send the shutdown command with parameters
	if err := sendShutdownCommand(socketPath, *cleanup); err != nil {
		return fmt.Errorf("failed to send shutdown command: %w", err)
	}

	log.Printf("Shutdown command sent, waiting for daemon to exit...")

	// Wait for daemon to exit (check socket becomes inactive)
	timeout := time.After(10 * time.Second)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			log.Printf("Warning: Daemon did not exit within timeout")
			fmt.Fprintf(os.Stderr, "Warning: Daemon may still be running\n")
			return nil

		case <-ticker.C:
			if !isSocketActive(socketPath) {
				log.Printf("Daemon has exited successfully")

				if *cleanup {
					fmt.Printf("Session '%s' has been stopped and cleaned up\n", sessionName)
				} else {
					fmt.Printf("Daemon stopped. Session '%s' remains available.\n", sessionName)
					fmt.Printf("To attach: opencode-tmux attach %s\n", sessionName)
					fmt.Printf("To destroy: tmux kill-session -t %s\n", sessionName)
				}
				return nil
			}
		}
	}
}

// sendShutdownCommand sends a shutdown command to the daemon via IPC
func sendShutdownCommand(socketPath string, cleanup bool) error {
	client := ipc.NewSocketClient(socketPath, "cli-stop", "controller")
	if err := client.Connect(); err != nil {
		return fmt.Errorf("failed to connect to daemon: %w", err)
	}
	defer client.Disconnect()

	// Build the command - we'll use a workaround since SendOrchestratorCommand
	// doesn't support params yet. We'll encode the cleanup flag in the command name.
	command := "shutdown"
	if cleanup {
		command = "shutdown:cleanup"
	}

	if err := client.SendOrchestratorCommand(command); err != nil {
		return fmt.Errorf("failed to send shutdown: %w", err)
	}

	return nil
}
