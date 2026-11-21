//go:build !windows

package subprocess

import (
	"fmt"
	"syscall"
)

// The tests of this package start processes that end by every signal 1 to 31 (the harness entry's tests end stubs and Node processes by QUIT, ILL, TRAP, ABRT, BUS, FPE, SEGV, SYS, XCPU and XFSZ), and a process that ends
// by one of them dumps core into its working directory where the kernel's core pattern says so: one multi-megabyte file per Node process per run, which for a Node process is its whole heap. The soft RLIMIT_CORE is 0 for
// the test process from the start and every child inherits it. The limit does not change what a row measures: a wait status of a core-dumping signal says Signaled with the signal, and CoreDump only if a file was written,
// and the harness tests compare the signal, not that flag. harnessNoCoreWritten pins that no process of those tests leaves a core file.
func init() {
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_CORE, &limit); err != nil {
		panic(fmt.Sprintf("reading RLIMIT_CORE: %v", err))
	}
	limit.Cur = 0
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &limit); err != nil {
		panic(fmt.Sprintf("setting the soft RLIMIT_CORE to 0: %v", err))
	}
}
