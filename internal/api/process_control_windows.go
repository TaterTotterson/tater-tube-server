//go:build windows

package api

import (
	"errors"
	"os"
)

func taterProcessSuspensionSupported() bool { return false }

func taterSuspendProcess(_ *os.Process) error { return errors.New("process suspension is unavailable") }
func taterResumeProcess(_ *os.Process) error  { return errors.New("process suspension is unavailable") }
