package subprocess

import (
	"testing"
	"time"
)

// requireExited waits for the process of a generation to end. It is not in generation_linger_test.go, which the node_extensions tag compiles out, because the
// test of a Go command that awaits a reload (reload_caller_packed_cell_test.go) needs it in a build without the Node runtime too.
func requireExited(t *testing.T, me *managedExt, what string) {
	t.Helper()
	exited := me.exited()
	if exited == nil && me.packedProcess != nil {
		// A member of the Node cell: the cell's process is the member's process.
		exited = me.packedProcess.startWait()
	}
	if exited == nil {
		t.Fatalf("%s: the generation has no process to wait for", what)
	}
	select {
	case <-exited:
	case <-time.After(testTimeout(t, 10*time.Second)):
		t.Fatalf("%s: the process is still running", what)
	}
}
