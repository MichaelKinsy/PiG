//go:build !unix && !windows

package nodespawn

import "os/exec"

// DetachFromTerminal does nothing where the platform has neither sessions nor
// detached console processes.
func DetachFromTerminal(*exec.Cmd) {}
