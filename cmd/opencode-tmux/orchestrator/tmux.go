package orchestrator

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/opencode/tmux_coder/cmd/opencode-tmux/config"
)

// SessionExists checks if a tmux session exists
func SessionExists(sessionName string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", sessionName)
	err := cmd.Run()
	return err == nil
}

// CreateTmuxSession creates a new tmux session
func CreateTmuxSession(cfg *config.OrchestratorConfig) error {
	log.Printf("Creating new tmux session: %s", cfg.SessionName)

	cmd := exec.Command("tmux", "new-session", "-d", "-s", cfg.SessionName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to create tmux session: %w", err)
	}

	log.Printf("Tmux session created: %s", cfg.SessionName)
	return nil
}

// KillTmuxSession kills an existing tmux session
func KillTmuxSession(sessionName string) error {
	log.Printf("Killing tmux session: %s", sessionName)

	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to kill tmux session: %w", err)
	}

	return nil
}

// AttachToSession attaches to a tmux session
func AttachToSession(sessionName string, detachKeys string) error {
	args := []string{"attach-session", "-t", sessionName}

	if detachKeys != "" {
		args = append(args, "-d", detachKeys)
	}

	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// AttachReadOnly attaches to tmux session in read-only mode
func AttachReadOnly(sessionName string, detachKeys string) error {
	args := []string{"attach-session", "-t", sessionName, "-r"}

	if detachKeys != "" {
		args = append(args, "-d", detachKeys)
	}

	cmd := exec.Command("tmux", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

// GetTmuxWindows returns a list of windows for a session
func GetTmuxWindows(sessionName string) ([]string, error) {
	cmd := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_id}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list windows: %w", err)
	}

	windows := strings.Split(strings.TrimSpace(string(output)), "\n")
	return windows, nil
}

// PaneInfo represents information about a tmux pane
type PaneInfo struct {
	ID     string
	Index  int
	Active bool
}

// GetTmuxPanes returns a list of panes for a session
func GetTmuxPanes(sessionName string) ([]PaneInfo, error) {
	cmd := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_id}:#{pane_index}:#{pane_active}")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list panes: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	panes := make([]PaneInfo, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		if len(parts) != 3 {
			continue
		}

		var index int
		fmt.Sscanf(parts[1], "%d", &index)

		panes = append(panes, PaneInfo{
			ID:     parts[0],
			Index:  index,
			Active: parts[2] == "1",
		})
	}

	return panes, nil
}

// ValidateExistingSession checks if existing session is compatible
func ValidateExistingSession(cfg *config.OrchestratorConfig) error {
	// Check if session has expected windows
	windows, err := GetTmuxWindows(cfg.SessionName)
	if err != nil {
		return err
	}

	if len(windows) == 0 {
		return fmt.Errorf("session has no windows")
	}

	// Check if session has panes
	panes, err := GetTmuxPanes(cfg.SessionName)
	if err != nil {
		return err
	}

	if len(panes) == 0 {
		return fmt.Errorf("session has no panes")
	}

	log.Printf("Existing session validated: %d windows, %d panes", len(windows), len(panes))
	return nil
}

// ImportExistingPanes imports pane IDs from existing session
func ImportExistingPanes(cfg *config.OrchestratorConfig) (map[string]string, error) {
	panes, err := GetTmuxPanes(cfg.SessionName)
	if err != nil {
		return nil, err
	}

	// Map panes to layout roles
	paneMapping := make(map[string]string)
	if cfg.Layout != nil {
		for i, pane := range panes {
			if i < len(cfg.Layout.Panels) {
				panelConfig := cfg.Layout.Panels[i]
				paneMapping[panelConfig.ID] = pane.ID
			}
		}
	}

	return paneMapping, nil
}

// IsTmuxAvailable checks if tmux command is available
func IsTmuxAvailable() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}
