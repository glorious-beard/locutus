//go:build unix

package mcp

import "syscall"

// detachSysProcAttr returns a SysProcAttr that puts the forked daemon
// into its own session via setsid. Without this the daemon would
// receive SIGINT / SIGHUP propagated from the bootstrapping CLI's
// terminal session, which would defeat the singleton-survives-CLI-
// teardown model.
func detachSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
