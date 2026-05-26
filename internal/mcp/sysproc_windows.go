//go:build windows

package mcp

import "syscall"

// detachSysProcAttr returns the SysProcAttr used for daemon spawn on
// Windows. Windows doesn't have setsid; CreateProcess flag
// CREATE_NEW_PROCESS_GROUP gets closest to "detach from the
// parent's signal group" — Ctrl+C in the parent's console does not
// propagate to the daemon. This keeps the singleton-survives-CLI-
// teardown model intact on Windows.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
	}
}
