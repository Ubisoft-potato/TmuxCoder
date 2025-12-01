package commands

import (
	"flag"
	"fmt"
	"os"
)

// CmdList implements the 'list' subcommand
func CmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	quiet := fs.Bool("quiet", false, "Only show session names")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: opencode-tmux list [options]\n\n")
		fmt.Fprintf(os.Stderr, "List all running sessions.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	sessions, err := findAllSessions()
	if err != nil {
		return fmt.Errorf("failed to find sessions: %w", err)
	}

	if len(sessions) == 0 {
		fmt.Println("No sessions found")
		return nil
	}

	if *quiet {
		// Quiet mode: only show names
		for _, session := range sessions {
			fmt.Println(session)
		}
		return nil
	}

	// Full mode: show table with status
	fmt.Printf("%-20s %-12s %-12s %s\n", "NAME", "TMUX", "DAEMON", "CLIENTS")
	fmt.Println("────────────────────────────────────────────────────────────")

	for _, session := range sessions {
		status := checkSessionStatus(session)

		tmuxStatus := "Stopped"
		if status.TmuxRunning {
			tmuxStatus = "Running"
		}

		daemonStatus := "Stopped"
		if status.DaemonRunning {
			daemonStatus = "Running"
		}

		// Determine overall status
		overallStatus := "Orphaned"
		if status.TmuxRunning && status.DaemonRunning {
			overallStatus = "Running"
		} else if !status.TmuxRunning && !status.DaemonRunning {
			overallStatus = "Stopped"
		}

		fmt.Printf("%-20s %-12s %-12s %d\n",
			session,
			tmuxStatus,
			daemonStatus,
			status.ClientCount)

		// Show hint for orphaned sessions
		if overallStatus == "Orphaned" {
			if status.TmuxRunning && !status.DaemonRunning {
				fmt.Printf("  → Orphaned session (daemon stopped), use: opencode-tmux start %s --reuse\n", session)
			}
		}
	}

	return nil
}
