//go:build linux

package nodespawn

// UseBinfmtMiscDir makes the spawns of this process read the binfmt_misc handlers from dir, where the kernel mounts them at /proc/sys/fs/binfmt_misc, and returns the function that
// puts the previous directory back. Every spawn of a program reads each enabled handler (SetProgram's execve check, miscErrno), about 14 allocations a handler, so the allocs/op of a
// benchmark that spawns follows the handlers the machine registered: 82 on a host that registers one, 153 on the nightly's runner. A benchmark that wants what a spawn costs, not the
// machine's, points it at an empty directory, which is a kernel without binfmt_misc. It is not safe to call while another goroutine spawns.
func UseBinfmtMiscDir(dir string) (restore func()) {
	previous := binfmtMiscDir
	binfmtMiscDir = dir
	return func() { binfmtMiscDir = previous }
}
