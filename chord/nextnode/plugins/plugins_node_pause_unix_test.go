//go:build !windows

package plugins

import (
	"syscall"
	"testing"
)

// pauseNode stops the Node process; the returned function continues it (it also runs when the test ends).
func pauseNode(t *testing.T, node *NodeHost) (resume func()) {
	t.Helper()
	process := node.cmd.Process
	if err := process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	resume = func() { _ = process.Signal(syscall.SIGCONT) }
	t.Cleanup(resume)
	return resume
}
