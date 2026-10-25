package testenv

import (
	"syscall"
	"testing"
)

// RequireForkLocked fails the test unless this process cannot start a child right now. Go forks under the write side of syscall.ForkLock, so a fork waits while any goroutine holds the lock for reading. Call it where a file is open for writing and this process will execute that file afterwards: a child forked then holds a copy of the descriptor until it calls exec, and the later exec fails with ETXTBSY. The test goroutine must be the only one forking, or a fork in progress satisfies the check.
func RequireForkLocked(t testing.TB) {
	t.Helper()
	if syscall.ForkLock.TryLock() {
		syscall.ForkLock.Unlock()
		t.Error("a fork could start now, while the file is open for writing: a child forked here keeps the file busy until it calls exec")
	}
}
