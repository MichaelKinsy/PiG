//go:build !unix

package tui

import (
	"os"
	"os/exec"
)

// openNoFollow opens file (Windows has no O_NOFOLLOW; the identity check against the confirmed file guards the swap).
func openNoFollow(file string) (*os.File, error) { return os.Open(file) }

// detach has nothing to do: a Windows child outlives its parent.
func detach(*exec.Cmd) {}
