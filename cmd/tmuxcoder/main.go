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

	// Get the directory where the binary is located
	execPath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to determine executable path: %v\n", err)
		os.Exit(1)
	}

	// Resolve symlinks
	execPath, err = filepath.EvalSymlinks(execPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to resolve symlink: %v\n", err)
		os.Exit(1)
	}

	// Get the project root (assuming binary is in cmd/tmuxcoder/dist/ or similar)
	binDir := filepath.Dir(execPath)
	projectRoot := findProjectRoot(binDir)
	if projectRoot == "" {
		fmt.Fprintf(os.Stderr, "Error: could not find project root (looking for scripts/start.sh)\n")
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

// findProjectRoot searches upward from the given directory to find the project root
func findProjectRoot(startDir string) string {
	dir := startDir
	for {
		scriptPath := filepath.Join(dir, "scripts", "start.sh")
		if _, err := os.Stat(scriptPath); err == nil {
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
    # Start tmuxcoder (builds and launches)
    tmuxcoder

    # Skip build step if binaries already exist
    tmuxcoder --skip-build

    # Attach to existing session without rebuilding
    tmuxcoder --attach-only

    # Use custom server
    tmuxcoder --server http://localhost:8080

ENVIRONMENT VARIABLES:
    OPENCODE_SERVER           OpenCode API server URL
    OPENCODE_SOCKET           IPC socket path (default: ~/.opencode/ipc.sock)
    OPENCODE_STATE            State file path (default: ~/.opencode/state.json)
    OPENCODE_TMUX_CONFIG      Config file path (default: ~/.opencode/tmux.yaml)
    OPENCODE_AUTO_SERVER_PORT Auto-start server port (default: 55306)

For more information, visit: https://github.com/yourusername/TmuxCoder
`
	fmt.Print(help)
}
