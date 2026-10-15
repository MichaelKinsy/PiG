//go:build !unix

package aotruntime

import (
	"errors"
	"os/exec"
)

type fileLock struct{}

func lockFile(string, bool) (*fileLock, bool, error) {
	return nil, false, errors.New("the aot runtime builds only on linux and darwin")
}

func (*fileLock) Unlock() {}

// ownProcessGroup bounds the wait for output pipes; without process groups nothing else is set.
func ownProcessGroup(cmd *exec.Cmd) { cmd.WaitDelay = processWaitDelay }

func endProcessGroup(*exec.Cmd) {}
