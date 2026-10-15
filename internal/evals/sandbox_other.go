//go:build !unix

package evals

import (
	"errors"
	"os/exec"
)

func chownTree(string, sandboxIdentity) error {
	return errors.New("The eval filesystem sandbox requires POSIX user APIs.")
}

func enterToolSandbox(_ *exec.Cmd, _ string, identity *sandboxIdentity) error {
	if identity == nil {
		return nil
	}
	return errors.New("The eval filesystem sandbox requires POSIX user APIs.")
}
