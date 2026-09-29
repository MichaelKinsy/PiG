//go:build linux

package nodespawn

import (
	"errors"
	"os"
	"runtime"
	"strings"
	"syscall"
)

// probeExecve is the errno of the execve of path by a child whose working
// directory is cwd, and whether the kernel could be asked, for a file whose
// fate only the kernel knows: one that PiG has no permission to read (the kernel
// reads the head of the file it executes whatever its mode), or an ELF file
// whose program header table is larger than a page (Linux 6.17 removed that
// limit). The probe executes path in a child that is traced from its start,
// which stops after the kernel has replaced its image and before the new
// program runs its first instruction, and kills it there; a failure of the execve is the result, and 0 is a program that starts.
// It reports false when it cannot trace (a sandbox that forbids ptrace).
// The child runs in cwd, because the kernel opens a relative interpreter, of a
// "#!" line or of an ELF PT_INTERP, relative to the working directory.
func probeExecve(path, cwd string) (syscall.Errno, bool) {
	// The child changes to cwd before it executes, so a relative path is made
	// absolute here, without cleaning, as inDirectory describes.
	if !strings.HasPrefix(path, "/") {
		wd, err := os.Getwd()
		if err != nil {
			return 0, false
		}
		path = inDirectory(wd, path)
	}
	// The tracer must be the thread that started the child.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pid, err := syscall.ForkExec(path, []string{path}, &syscall.ProcAttr{
		Dir: cwd,
		Env: []string{},
		Sys: &syscall.SysProcAttr{Ptrace: true},
	})
	if err != nil {
		if errno, ok := errors.AsType[syscall.Errno](err); ok && errno != syscall.EPERM && errno != syscall.ENOSYS {
			return errno, true
		}
		return 0, false
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	for {
		var status syscall.WaitStatus
		_, err := syscall.Wait4(pid, &status, syscall.WALL, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || status.Exited() || status.Signaled() {
			return 0, true
		}
	}
}
