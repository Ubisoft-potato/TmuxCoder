//go:build darwin

package ipc

import (
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/opencode/tmux_coder/internal/interfaces"
)

// getRequesterFromConnImpl extracts peer credentials using LOCAL_PEERCRED
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

	// macOS uses LOCAL_PEERCRED instead of SO_PEERCRED
	// Structure: struct xucred from sys/ucred.h
	type xucred struct {
		Version uint32
		Uid     uint32
		Gid     uint32
		Ngroups int16
		Groups  [16]uint32
	}

	var cred xucred
	credLen := uint32(unsafe.Sizeof(cred))

	// macOS socket level and option constants
	const SOL_LOCAL = 0 // Socket level for local sockets
	const LOCAL_PEERCRED = 0x001

	_, _, errno := syscall.Syscall6(
		syscall.SYS_GETSOCKOPT,
		uintptr(file.Fd()),
		uintptr(SOL_LOCAL),
		uintptr(LOCAL_PEERCRED),
		uintptr(unsafe.Pointer(&cred)),
		uintptr(unsafe.Pointer(&credLen)),
		0,
	)

	if errno != 0 {
		return nil, fmt.Errorf("failed to get peer credentials: %v", errno)
	}

	// Lookup username
	username := ""
	if u, err := user.LookupId(strconv.FormatUint(uint64(cred.Uid), 10)); err == nil {
		username = u.Username
	}

	// Get hostname
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	// Note: macOS LOCAL_PEERCRED doesn't provide PID
	// We set PID to -1 to indicate unavailable
	return &interfaces.IpcRequester{
		UID:      cred.Uid,
		GID:      cred.Gid,
		PID:      -1, // Not available on macOS
		Username: username,
		Hostname: hostname,
	}, nil
}
