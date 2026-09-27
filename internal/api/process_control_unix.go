//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package api

import (
	"os"
	"syscall"
)

func taterProcessSuspensionSupported() bool { return true }

func taterSuspendProcess(process *os.Process) error {
	if process == nil {
		return os.ErrInvalid
	}
	return process.Signal(syscall.SIGSTOP)
}

func taterResumeProcess(process *os.Process) error {
	if process == nil {
		return os.ErrInvalid
	}
	return process.Signal(syscall.SIGCONT)
}
