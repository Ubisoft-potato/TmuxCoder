package interfaces

// OrchestratorControl exposes control operations that can be invoked over IPC.
type OrchestratorControl interface {
	// ReloadLayout triggers the orchestrator to reload the tmux layout configuration
	// and apply it to the running session without restarting panel processes.
	ReloadLayout() error

	// Shutdown triggers graceful shutdown of the orchestrator daemon.
	// If cleanup is true, the tmux session will also be destroyed.
	Shutdown(cleanup bool) error
}
