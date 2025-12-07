package permission

import (
	"fmt"

	"github.com/opencode/tmux_coder/internal/interfaces"
)

// PermissionLevel defines who can perform an operation
type PermissionLevel string

const (
	PermissionOwner PermissionLevel = "owner" // Only session owner
	PermissionGroup PermissionLevel = "group" // Same group as owner
	PermissionAny   PermissionLevel = "any"   // Anyone with socket access
)

// Operation represents an IPC operation that requires permission
type Operation string

const (
	OperationShutdown     Operation = "shutdown"
	OperationReloadLayout Operation = "reload_layout"
	OperationGetStatus    Operation = "get_status"
	OperationGetClients   Operation = "get_clients"
)

// Policy defines permission requirements for operations
type Policy struct {
	Shutdown     PermissionLevel
	ReloadLayout PermissionLevel
	GetStatus    PermissionLevel
	GetClients   PermissionLevel
}

// DefaultPolicy returns the default permission policy
func DefaultPolicy() *Policy {
	return &Policy{
		Shutdown:     PermissionOwner, // Only owner can shutdown
		ReloadLayout: PermissionGroup, // Same group can reload
		GetStatus:    PermissionAny,   // Anyone can view status
		GetClients:   PermissionAny,   // Anyone can list clients
	}
}

// Checker checks if a requester has permission for an operation
type Checker struct {
	sessionOwner interfaces.SessionOwner
	policy       *Policy
}

// NewChecker creates a new permission checker
func NewChecker(owner interfaces.SessionOwner, policy *Policy) *Checker {
	if policy == nil {
		policy = DefaultPolicy()
	}

	return &Checker{
		sessionOwner: owner,
		policy:       policy,
	}
}

// CheckPermission verifies if requester can perform operation
func (c *Checker) CheckPermission(op Operation, requester *interfaces.IpcRequester) error {
	if requester == nil {
		return fmt.Errorf("permission denied: requester identity unknown")
	}

	var required PermissionLevel

	switch op {
	case OperationShutdown:
		required = c.policy.Shutdown
	case OperationReloadLayout:
		required = c.policy.ReloadLayout
	case OperationGetStatus:
		required = c.policy.GetStatus
	case OperationGetClients:
		required = c.policy.GetClients
	default:
		return fmt.Errorf("unknown operation: %s", op)
	}

	return c.checkLevel(required, requester)
}

// checkLevel verifies if requester meets the permission level
func (c *Checker) checkLevel(level PermissionLevel, requester *interfaces.IpcRequester) error {
	switch level {
	case PermissionAny:
		// Anyone with socket access can perform
		return nil

	case PermissionGroup:
		// Must be same group as owner
		if requester.GID == c.sessionOwner.GID {
			return nil
		}
		// Owner always has permission
		if requester.UID == c.sessionOwner.UID {
			return nil
		}
		return fmt.Errorf("permission denied: operation requires group membership (GID %d)", c.sessionOwner.GID)

	case PermissionOwner:
		// Must be session owner
		if requester.UID == c.sessionOwner.UID {
			return nil
		}
		return fmt.Errorf("permission denied: operation requires session owner (UID %d, user %s)",
			c.sessionOwner.UID, c.sessionOwner.Username)

	default:
		return fmt.Errorf("invalid permission level: %s", level)
	}
}

// GetSessionOwner returns the session owner
func (c *Checker) GetSessionOwner() interfaces.SessionOwner {
	return c.sessionOwner
}

// UpdatePolicy updates the permission policy
func (c *Checker) UpdatePolicy(policy *Policy) {
	if policy != nil {
		c.policy = policy
	}
}
