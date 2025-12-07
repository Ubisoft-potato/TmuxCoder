package supervision

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// EnvChecker verifies that pane processes have the expected environment variables
// This is crucial for detecting orphaned sessions that need environment updates
type EnvChecker struct {
	tmuxCommand  string
	expectedVars map[string]string
}

// NewEnvChecker creates a new environment checker with expected variables
func NewEnvChecker(tmuxCommand string, expectedVars map[string]string) *EnvChecker {
	return &EnvChecker{
		tmuxCommand:  tmuxCommand,
		expectedVars: expectedVars,
	}
}

// NeedsEnvUpdate checks if a pane's process has stale environment variables
//
// This detects orphaned sessions where the daemon was restarted but panes
// still reference the old socket path or other environment variables.
//
// Returns true if any expected variable is missing or has the wrong value.
func (ec *EnvChecker) NeedsEnvUpdate(paneTarget string) bool {
	// Get the PID of the process running in the pane
	cmd := exec.Command(ec.tmuxCommand, "display-message", "-p", "-t", paneTarget, "#{pane_pid}")
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[EnvCheck] Failed to get PID for pane %s: %v", paneTarget, err)
		return true
	}

	pidStr := strings.TrimSpace(string(output))
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		log.Printf("[EnvCheck] Invalid PID '%s' for pane %s: %v", pidStr, paneTarget, err)
		return true
	}

	// Use platform-specific method to check environment
	if runtime.GOOS == "darwin" {
		return ec.needsEnvUpdateMacOS(pid)
	}
	return ec.needsEnvUpdateLinux(pid)
}

// needsEnvUpdateLinux checks environment variables on Linux using /proc filesystem
func (ec *EnvChecker) needsEnvUpdateLinux(pid int) bool {
	// Read the process environment from /proc
	envPath := fmt.Sprintf("/proc/%d/environ", pid)
	data, err := os.ReadFile(envPath)
	if err != nil {
		log.Printf("[EnvCheck] Failed to read %s: %v", envPath, err)
		return true
	}

	// Parse null-separated environment variables
	environ := strings.Split(string(data), "\x00")
	envMap := make(map[string]string)
	for _, env := range environ {
		if env == "" {
			continue
		}
		parts := strings.SplitN(env, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	// Check each expected variable
	for key, expected := range ec.expectedVars {
		actual, exists := envMap[key]
		if !exists {
			log.Printf("[EnvCheck] Missing variable: %s", key)
			return true
		}
		if actual != expected {
			log.Printf("[EnvCheck] Mismatch: %s expected=%s actual=%s", key, expected, actual)
			return true
		}
	}

	log.Printf("[EnvCheck] Process %d has correct environment", pid)
	return false
}

// needsEnvUpdateMacOS checks environment variables on macOS using ps command
func (ec *EnvChecker) needsEnvUpdateMacOS(pid int) bool {
	// Use ps with 'eww' flags to show full environment
	cmd := exec.Command("ps", "eww", "-p", fmt.Sprintf("%d", pid))
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[EnvCheck] Failed to run ps for PID %d: %v", pid, err)
		return true
	}

	outputStr := string(output)

	// Check each expected variable appears in the output
	for key, expected := range ec.expectedVars {
		searchStr := fmt.Sprintf("%s=%s", key, expected)
		if !strings.Contains(outputStr, searchStr) {
			log.Printf("[EnvCheck] Not found in ps output: %s", searchStr)
			return true
		}
	}

	log.Printf("[EnvCheck] Process %d has correct environment", pid)
	return false
}

// CheckAllPanes checks multiple panes and returns which ones need updates
//
// Returns a map of pane target -> needs update boolean
func (ec *EnvChecker) CheckAllPanes(paneTargets []string) map[string]bool {
	result := make(map[string]bool, len(paneTargets))
	for _, target := range paneTargets {
		result[target] = ec.NeedsEnvUpdate(target)
	}
	return result
}
