package supervision

import (
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// PaneHealth represents the health status of a tmux pane
type PaneHealth int

const (
	PaneHealthy PaneHealth = iota // Process is running normally
	PaneDead                      // Process has exited (pane_dead=1)
	PaneZombie                    // Process PID is invalid or zombie
	PaneMissing                   // Pane does not exist in session
)

// String returns a human-readable representation of the health status
func (h PaneHealth) String() string {
	switch h {
	case PaneHealthy:
		return "Healthy"
	case PaneDead:
		return "Dead"
	case PaneZombie:
		return "Zombie"
	case PaneMissing:
		return "Missing"
	default:
		return "Unknown"
	}
}

// PaneHealthChecker performs health checks on tmux panes
type PaneHealthChecker struct {
	tmuxCommand string
	sessionName string
}

// NewPaneHealthChecker creates a new health checker for the given session
func NewPaneHealthChecker(tmuxCommand, sessionName string) *PaneHealthChecker {
	return &PaneHealthChecker{
		tmuxCommand: tmuxCommand,
		sessionName: sessionName,
	}
}

// CheckPaneHealth checks the health status of a single pane
//
// The check follows this process:
// 1. Verify the pane exists in the session
// 2. Check tmux's pane_dead flag
// 3. Verify the process PID is valid and running
//
// Returns the appropriate PaneHealth status
func (hc *PaneHealthChecker) CheckPaneHealth(paneTarget string) PaneHealth {
	// Step 1: Check if pane exists in session
	cmd := exec.Command(hc.tmuxCommand, "list-panes", "-t", hc.sessionName, "-F", "#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[Health] Failed to list panes for session %s: %v", hc.sessionName, err)
		return PaneMissing
	}

	paneIDs := strings.Split(strings.TrimSpace(string(output)), "\n")
	found := false
	for _, id := range paneIDs {
		if strings.TrimSpace(id) == paneTarget {
			found = true
			break
		}
	}
	if !found {
		log.Printf("[Health] Pane %s not found in session %s", paneTarget, hc.sessionName)
		return PaneMissing
	}

	// Step 2: Check tmux's pane_dead flag
	cmd = exec.Command(hc.tmuxCommand, "display-message", "-p", "-t", paneTarget, "#{pane_dead}")
	output, err = cmd.Output()
	if err != nil {
		log.Printf("[Health] Failed to check pane_dead for %s: %v", paneTarget, err)
		return PaneMissing
	}

	if strings.TrimSpace(string(output)) == "1" {
		log.Printf("[Health] Pane %s is marked as dead by tmux", paneTarget)
		return PaneDead
	}

	// Step 3: Verify process PID is valid
	cmd = exec.Command(hc.tmuxCommand, "display-message", "-p", "-t", paneTarget, "#{pane_pid}")
	output, err = cmd.Output()
	if err != nil || strings.TrimSpace(string(output)) == "" {
		log.Printf("[Health] Failed to get PID for pane %s: %v", paneTarget, err)
		return PaneZombie
	}

	pidStr := strings.TrimSpace(string(output))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		log.Printf("[Health] Invalid PID '%s' for pane %s: %v", pidStr, paneTarget, err)
		return PaneZombie
	}

	// Step 4: Verify process exists and is alive
	process, err := os.FindProcess(pid)
	if err != nil {
		log.Printf("[Health] Process %d not found for pane %s: %v", pid, paneTarget, err)
		return PaneZombie
	}

	// Send signal 0 to check if process is alive (doesn't actually send a signal)
	if err := process.Signal(os.Signal(nil)); err != nil {
		log.Printf("[Health] Process %d is not alive for pane %s: %v", pid, paneTarget, err)
		return PaneZombie
	}

	log.Printf("[Health] Pane %s is healthy (PID %d)", paneTarget, pid)
	return PaneHealthy
}

// CheckAllPanesHealth checks the health status of multiple panes
//
// Returns a map of pane target -> health status
func (hc *PaneHealthChecker) CheckAllPanesHealth(paneTargets []string) map[string]PaneHealth {
	result := make(map[string]PaneHealth, len(paneTargets))
	for _, target := range paneTargets {
		result[target] = hc.CheckPaneHealth(target)
	}
	return result
}
