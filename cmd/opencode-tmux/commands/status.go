package commands

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

// CmdStatus implements the 'status' subcommand
func CmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux status [options] [session-name]\n\n")
		fmt.Fprintf(os.Stderr, "View session status.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	sessionName := getSessionName(fs.Args())

	// Gather session information
	status := checkSessionStatus(sessionName)
	socketPath := getSocketPath(sessionName)
	pidPath := getPIDPath(sessionName)

	// Get client details if tmux is running
	var clients []ClientInfo
	if status.TmuxRunning {
		var err error
		clients, err = getConnectedClients(sessionName)
		if err != nil {
			// Non-fatal error, continue
			clients = []ClientInfo{}
		}
	}

	// Determine overall status
	overallStatus := "Unknown"
	if status.TmuxRunning && status.DaemonRunning {
		overallStatus = "Running"
	} else if status.TmuxRunning && !status.DaemonRunning {
		overallStatus = "Orphaned"
	} else if !status.TmuxRunning && !status.DaemonRunning {
		overallStatus = "Stopped"
	} else if !status.TmuxRunning && status.DaemonRunning {
		overallStatus = "Daemon-Only"
	}

	if *jsonOutput {
		// JSON output
		output := map[string]interface{}{
			"session_name":    sessionName,
			"status":          overallStatus,
			"tmux_running":    status.TmuxRunning,
			"daemon_running":  status.DaemonRunning,
			"client_count":    status.ClientCount,
			"clients":         clients,
			"socket_path":     socketPath,
			"pid_path":        pidPath,
		}

		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	// Human-readable output
	fmt.Printf("Session: %s\n", sessionName)
	fmt.Printf("Status: %s\n", overallStatus)
	fmt.Println("────────────────────────────────────────")

	if status.TmuxRunning {
		fmt.Printf("Tmux Session: ✓ Running\n")
	} else {
		fmt.Printf("Tmux Session: ✗ Not running\n")
	}

	if status.DaemonRunning {
		fmt.Printf("Orchestrator Daemon: ✓ Running\n")
	} else {
		fmt.Printf("Orchestrator Daemon: ✗ Not running\n")
	}

	fmt.Printf("Connected Clients: %d\n", status.ClientCount)
	if len(clients) > 0 {
		for _, client := range clients {
			// Convert timestamp to readable format
			timestamp := client.ConnectedAt
			if ts, err := time.Parse(time.RFC3339, timestamp); err == nil {
				timestamp = ts.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("  - %s (PID %s, connected: %s)\n", client.TTY, client.PID, timestamp)
		}
	}

	fmt.Println()
	fmt.Printf("Socket Path: %s\n", socketPath)
	fmt.Printf("PID Path: %s\n", pidPath)

	// Show hints based on status
	if overallStatus == "Orphaned" {
		fmt.Println()
		fmt.Printf("⚠ Session is orphaned (tmux running but daemon stopped)\n")
		fmt.Printf("To restore management: opencode-tmux start %s --reuse\n", sessionName)
		fmt.Printf("To destroy session:    opencode-tmux stop %s --cleanup\n", sessionName)
	} else if overallStatus == "Stopped" {
		fmt.Println()
		fmt.Printf("Session is not running\n")
		fmt.Printf("To start: opencode-tmux start %s\n", sessionName)
	}

	return nil
}
