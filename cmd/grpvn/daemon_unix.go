//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func detachDaemon(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
