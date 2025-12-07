//go:build linux

package ipc

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/opencode/tmux_coder/internal/interfaces"
)

// getRequesterFromConnImpl extracts peer credentials using SO_PEERCRED
func getRequesterFromConnImpl(conn net.Conn) (*interfaces.IpcRequester, error) {
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		return nil, fmt.Errorf("not a unix socket connection")
	}

	// Get file descriptor
	file, err := unixConn.File()
	if err != nil {
		return nil, fmt.Errorf("failed to get connection file: %w", err)
	}
	defer file.Close()

	// Get peer credentials using SO_PEERCRED
	ucred, err := syscall.GetsockoptUcred(
		int(file.Fd()),
		syscall.SOL_SOCKET,
		syscall.SO_PEERCRED,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get peer credentials: %w", err)
	}

	// Lookup username
	username := ""
	if u, err := user.LookupId(strconv.FormatUint(uint64(ucred.Uid), 10)); err == nil {
		username = u.Username
	}

	// Get hostname
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	return &interfaces.IpcRequester{
		UID:      ucred.Uid,
		GID:      ucred.Gid,
		PID:      int(ucred.Pid),
		Username: username,
		Hostname: hostname,
	}, nil
}
