package testenv

import (
	"strings"
	"sync"
	"testing"
)

// unprivilegedTests holds the names of running tests that called RunUnprivileged.
var unprivilegedTests sync.Map

// RunUnprivileged makes the calling test run without root's permission bypass. When the process is root, it reruns only the calling test in a child process as the unprivileged "nobody" user, fails with the child's output unless the child passed, and returns true; the caller then returns without repeating the test. Otherwise it returns false and the test continues in this process. Call it before any other setup. The rerun has a fresh HOME and TMPDIR and cannot read package-relative files.
func RunUnprivileged(t *testing.T) bool {
	t.Helper()
	name := t.Name()
	unprivilegedTests.Store(name, struct{}{})
	t.Cleanup(func() { unprivilegedTests.Delete(name) })
	return runUnprivileged(t)
}

// requireUnprivileged fails a test that uses a permission fixture without RunUnprivileged, on every host, so a missing call cannot pass as a non-root run and skip or pass vacuously as root.
func requireUnprivileged(t testing.TB) {
	t.Helper()
	for name := t.Name(); ; {
		if _, ok := unprivilegedTests.Load(name); ok {
			return
		}
		i := strings.LastIndexByte(name, '/')
		if i < 0 {
			break
		}
		name = name[:i]
	}
	t.Fatal("permission fixtures require testenv.RunUnprivileged at the start of the test, because root bypasses permission bits")
}
