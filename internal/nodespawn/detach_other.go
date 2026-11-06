//go:build !unix && !windows

package nodespawn

import "os/exec"

// DetachFromTerminal does nothing where the platform has neither sessions nor
// console windows.
func DetachFromTerminal(*exec.Cmd) {}
