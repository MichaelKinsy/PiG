//go:build !windows

package nodespawn

import (
	"syscall"
	"testing"
)

// The kernel's own answer decides a program header table over a page whenever
// it can be asked, whatever its release says: a pre-6.17 release that executes
// the file (a backport, or a sandbox reporting its own release) starts it, and
// only a kernel that cannot be asked is judged by its release
// (fs/binfmt_elf.c load_elf_phdrs; Linux 6.17 removed the ELF_MIN_ALIGN limit).
func TestOverPageErrnoTrustsTheKernel(t *testing.T) {
	for _, c := range []struct {
		name        string
		probed      syscall.Errno
		asked       bool
		readsLarge  bool
		want        syscall.Errno
		wantDecided bool
	}{
		{"an old release that starts the file", 0, true, false, 0, true},
		{"a new release that starts the file", 0, true, true, 0, true},
		{"an old kernel that rejects the table", syscall.ENOEXEC, true, false, syscall.ENOEXEC, true},
		{"a new kernel without the interpreter", syscall.ENOENT, true, true, syscall.ENOENT, true},
		{"a new release that rejects the table", syscall.ENOEXEC, true, true, syscall.ENOEXEC, true},
		{"an old release that cannot be asked", 0, false, false, syscall.ENOEXEC, true},
		{"a new release that cannot be asked", 0, false, true, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, decided := overPageErrno(c.probed, c.asked, c.readsLarge)
			if got != c.want || decided != c.wantDecided {
				t.Errorf("overPageErrno(%v, %v, %v) = %v, %v; want %v, %v", c.probed, c.asked, c.readsLarge, got, decided, c.want, c.wantDecided)
			}
		})
	}
}
