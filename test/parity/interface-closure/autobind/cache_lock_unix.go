//go:build unix

package main

import (
	"os"
	"syscall"
)

// lock takes an exclusive advisory lock on the entry so concurrent lanes compute a key once; the others wait and then read the result.
// The lock is released when the process exits, so a killed lane never blocks the rest. Where the file system refuses locks the caller
// computes without one: the key is content-addressed, so duplicate work is wasted but never wrong.
func (c *ledgerCache) lock(kind, key string) (release func()) {
	path := c.entryDir(kind, key) + ".lock"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o664)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		c.logf("ledger-cache: %s-%s is being computed by another process; waiting", kind, key[:8])
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
			_ = f.Close()
			return func() {}
		}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}
