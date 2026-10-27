//go:build linux

package daemon

// pi: packages/env/daemon/src/main.rs

import (
	"syscall"
	"testing"
)

// D197: the Go runtime raises the soft limit of open files to one below the hard one (syscall/rlimit.go) when the process
// starts, so PiG's daemon (this test binary runs under the same runtime) has nearly the hard limit. Pi's daemon keeps the
// soft limit it inherits, which TestDaemonDescriptorLimitIsTheHardLimitUnlikePisDaemon (env) shows against Pi's own daemon.
func TestDescriptorLimitIsTheHardLimit(t *testing.T) {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err != nil {
		t.Fatal(err)
	}
	if limit.Cur < limit.Max-1 {
		t.Fatalf("the soft limit of open files is %d, want the hard limit %d or one below it", limit.Cur, limit.Max)
	}
}
