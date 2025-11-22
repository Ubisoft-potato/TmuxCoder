package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const version = "1.0.0"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "-v", "--version":
			fmt.Printf("tmuxcoder v%s\n", version)
			return
		case "help", "-h", "--help":
			printHelp()
			return
		}
	}

	// Find project root - try multiple strategies
	projectRoot := findProjectRoot()
	if projectRoot == "" {
		fmt.Fprintf(os.Stderr, "Error: could not find project root (looking for scripts/start.sh)\n")
		fmt.Fprintf(os.Stderr, "\nPlease run tmuxcoder from the project directory, or set TMUXCODER_ROOT:\n")
		fmt.Fprintf(os.Stderr, "  cd /path/to/TmuxCoder\n")
		fmt.Fprintf(os.Stderr, "  ./tmuxcoder\n")
		fmt.Fprintf(os.Stderr, "\nOr:\n")
		fmt.Fprintf(os.Stderr, "  export TMUXCODER_ROOT=/path/to/TmuxCoder\n")
		fmt.Fprintf(os.Stderr, "  tmuxcoder\n")
		os.Exit(1)
	}

	// Path to the start script
	startScript := filepath.Join(projectRoot, "scripts", "start.sh")
	if _, err := os.Stat(startScript); os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "Error: start script not found at %s\n", startScript)
		os.Exit(1)
	}

	// Prepare arguments for the start script
	var args []string
	if len(os.Args) > 1 {
		args = os.Args[1:]
	}

	// Execute the start script
	cmd := exec.Command(startScript, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "Error: failed to execute start script: %v\n", err)
		os.Exit(1)
	}
}

// findProjectRoot tries multiple strategies to find the project root
func findProjectRoot() string {
	// Strategy 1: Check TMUXCODER_ROOT environment variable
	if root := os.Getenv("TMUXCODER_ROOT"); root != "" {
		if isProjectRoot(root) {
			return root
		}
	}

	// Strategy 2: Check current working directory
	if cwd, err := os.Getwd(); err == nil {
		if isProjectRoot(cwd) {
			return cwd
		}
		// Also try searching upward from cwd
		if root := searchUpward(cwd); root != "" {
			return root
		}
	}

	// Strategy 3: Try to find from executable location (for when running ./tmuxcoder)
	if execPath, err := os.Executable(); err == nil {
		execPath, _ = filepath.EvalSymlinks(execPath)
		execDir := filepath.Dir(execPath)

		// If running from project directory (./tmuxcoder)
		if isProjectRoot(execDir) {
			return execDir
		}

		// Try searching upward from executable location
		if root := searchUpward(execDir); root != "" {
			return root
		}
	}

	return ""
}

// isProjectRoot checks if a directory is the project root
func isProjectRoot(dir string) bool {
	scriptPath := filepath.Join(dir, "scripts", "start.sh")
	_, err := os.Stat(scriptPath)
	return err == nil
}

// searchUpward searches for project root by going up the directory tree
func searchUpward(startDir string) string {
	dir := startDir
	for {
		if isProjectRoot(dir) {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached filesystem root
			return ""
		}
		dir = parent
	}
}

func printHelp() {
	help := `tmuxcoder - AI-powered coding orchestrator with tmux

USAGE:
    tmuxcoder [OPTIONS]

OPTIONS:
    -h, --help           Show this help message
    -v, --version        Show version information
    --skip-build         Skip the Go build step
    --server <URL>       Set OPENCODE_SERVER (default auto-start on 127.0.0.1:55306)
    --attach-only        Attach to existing tmux session only
    -- <args>            Pass additional arguments to opencode-tmux

EXAMPLES:
    # Start tmuxcoder (from project directory)
    cd /path/to/TmuxCoder
    tmuxcoder

    # Or set TMUXCODER_ROOT to run from anywhere
    export TMUXCODER_ROOT=/path/to/TmuxCoder
    tmuxcoder

    # Skip build step if binaries already exist
    tmuxcoder --skip-build

    # Attach to existing session without rebuilding
    tmuxcoder --attach-only

    # Use custom server
    tmuxcoder --server http://localhost:8080

ENVIRONMENT VARIABLES:
    TMUXCODER_ROOT            Path to TmuxCoder project directory
    OPENCODE_SERVER           OpenCode API server URL
    OPENCODE_SOCKET           IPC socket path (default: ~/.opencode/ipc.sock)
    OPENCODE_STATE            State file path (default: ~/.opencode/state.json)
    OPENCODE_TMUX_CONFIG      Config file path (default: ~/.opencode/tmux.yaml)
    OPENCODE_AUTO_SERVER_PORT Auto-start server port (default: 55306)

For more information, visit: https://github.com/yourusername/TmuxCoder
`
	fmt.Print(help)
}
