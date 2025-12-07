package ipc

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"time"

	"github.com/opencode/tmux_coder/internal/interfaces"
)

// GetRequesterFromConn extracts requester identity from connection
// This function is platform-specific (see credentials_linux.go, credentials_darwin.go)
func GetRequesterFromConn(conn net.Conn) (*interfaces.IpcRequester, error) {
	return getRequesterFromConnImpl(conn)
}

// ToSessionOwner converts IpcRequester to SessionOwner
func ToSessionOwner(req *interfaces.IpcRequester) interfaces.SessionOwner {
	return interfaces.SessionOwner{
		UID:       req.UID,
		GID:       req.GID,
		Username:  req.Username,
		Hostname:  req.Hostname,
		StartedAt: time.Now(),
	}
}

// GetCurrentUser returns information about the current process user
func GetCurrentUser() (*interfaces.IpcRequester, error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("failed to get current user: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	uid, _ := strconv.ParseUint(currentUser.Uid, 10, 32)
	gid, _ := strconv.ParseUint(currentUser.Gid, 10, 32)

	return &interfaces.IpcRequester{
		UID:      uint32(uid),
		GID:      uint32(gid),
		PID:      os.Getpid(),
		Username: currentUser.Username,
		Hostname: hostname,
	}, nil
}
