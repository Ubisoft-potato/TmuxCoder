package panel

import (
	"fmt"
	"sync"
)

// Constructor creates a new panel instance.
type Constructor func() Panel

type registryEntry struct {
	constructor Constructor
	metadata    Metadata
}

var (
	registry   = map[string]registryEntry{}
	registryMu sync.RWMutex
)

// Register adds a panel implementation to the global registry.
func Register(meta Metadata, ctor Constructor) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[meta.ID] = registryEntry{
		constructor: ctor,
		metadata:    meta,
	}
}

// MustRegister is like Register but panics on duplicate registration.
func MustRegister(meta Metadata, ctor Constructor) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[meta.ID]; exists {
		panic(fmt.Sprintf("panel %q already registered", meta.ID))
	}
	registry[meta.ID] = registryEntry{
		constructor: ctor,
		metadata:    meta,
	}
}

// Resolve returns a new panel instance and its metadata by ID.
func Resolve(id string) (Panel, Metadata, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	entry, ok := registry[id]
	if !ok {
		return nil, Metadata{}, fmt.Errorf("panel %q not registered", id)
	}

	return entry.constructor(), entry.metadata, nil
}

// List returns metadata for all registered panels.
func List() []Metadata {
	registryMu.RLock()
	defer registryMu.RUnlock()

	out := make([]Metadata, 0, len(registry))
	for _, entry := range registry {
		out = append(out, entry.metadata)
	}
	return out
}
