package panel

import (
	"context"
	"log"

	opencode "github.com/sst/opencode-sdk-go"
)

// RuntimeDeps captures shared services that can be injected into panels when
// they are launched by the orchestrator.
type RuntimeDeps struct {
	Context    context.Context
	Logger     *log.Logger
	HTTPClient *opencode.Client
	SocketPath string
	ServerURL  string
	PanelID    string
}

// Metadata describes a registered panel implementation.
type Metadata struct {
	ID             string
	DisplayName    string
	Version        string
	Capabilities   []string
	DefaultCommand []string
}

// HealthStatus represents the current health of a panel instance.
type HealthStatus struct {
	Healthy bool
	Reason  string
}

// Panel defines the lifecycle contract for a UI panel managed by the orchestrator.
type Panel interface {
	// Init prepares panel resources using injected dependencies.
	Init(deps RuntimeDeps) error
	// Run starts the panel main loop and blocks until completion or error.
	Run() error
	// Shutdown attempts to gracefully stop the panel.
	Shutdown(ctx context.Context) error
	// Health reports current status to inform restart policies.
	Health() HealthStatus
	// Metadata returns static information about this panel implementation.
	Metadata() Metadata
}
