// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

var mappedFileSink byte

func TestProcessMemoryReadsThisProcess(t *testing.T) {
	memory := ProcessMemory(t, os.Getpid())
	if memory.Rss <= 0 || memory.Pss <= 0 || memory.Pss > memory.Rss || memory.Anon <= 0 || memory.Anon > memory.Rss {
		t.Fatalf("%+v", memory)
	}
}

// The gated figure is Anon: pages the process wrote must raise it, and file pages it only mapped and read must raise Rss but not Anon.
func TestProcessMemoryAnonCountsWrittenPagesAndNotMappedFiles(t *testing.T) {
	const size = 16 << 20
	page := os.Getpagesize()

	// A sparse file: the test must not allocate a 16 MB buffer of its own, whose pages (huge pages, where the kernel's THP policy is "always") would count as the anonymous growth this test is measuring.
	path := filepath.Join(t.TempDir(), "mapped")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	before := ProcessMemory(t, os.Getpid())
	mapped, err := syscall.Mmap(int(file.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(mapped); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < size; i += page {
		mappedFileSink += mapped[i] // a package variable, so the compiler cannot drop the reads that fault the pages in
	}
	afterRead := ProcessMemory(t, os.Getpid())
	if grew := afterRead.Rss - before.Rss; grew < size*3/4 {
		t.Fatalf("Rss grew %.0f B after reading %d B of a mapped file, want at least three quarters of it", grew, size)
	}
	if grew := afterRead.Anon - before.Anon; grew > size/4 {
		t.Fatalf("Anon grew %.0f B after only reading a mapped file of %d B", grew, size)
	}

	heap, err := syscall.Mmap(-1, 0, size, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_ANON|syscall.MAP_PRIVATE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := syscall.Munmap(heap); err != nil {
			t.Error(err)
		}
	})
	for i := 0; i < size; i += page {
		heap[i] = 1
	}
	afterWrite := ProcessMemory(t, os.Getpid())
	if grew := afterWrite.Anon - afterRead.Anon; grew < size*3/4 {
		t.Fatalf("Anon grew %.0f B after writing %d B of anonymous pages, want at least three quarters of it", grew, size)
	}
}

// The memory gates measure processes the test process starts, so the setting must reach them: THP_enabled in /proc/<pid>/status is 1 until PR_SET_THP_DISABLE is set, and a child inherits it across fork and exec.
func TestDisableTransparentHugePagesReachesChildren(t *testing.T) {
	status := func(pid string) string {
		t.Helper()
		data, err := os.ReadFile("/proc/" + pid + "/status")
		if err != nil {
			t.Fatal(err)
		}
		//portlint:allow pathseparators the kernel writes /proc/<pid>/status with \n line ends only; Linux-only test
		for line := range strings.SplitSeq(string(data), "\n") {
			if value, ok := strings.CutPrefix(line, "THP_enabled:"); ok {
				return strings.TrimSpace(value)
			}
		}
		return ""
	}
	if status("self") == "" {
		t.Skip("this kernel has no transparent huge pages: /proc/self/status has no THP_enabled line, so there is nothing to disable")
	}
	t.Cleanup(func() {
		if err := unix.Prctl(unix.PR_SET_THP_DISABLE, 0, 0, 0, 0); err != nil {
			t.Error(err)
		}
	})
	if err := DisableTransparentHugePages(); err != nil {
		t.Fatal(err)
	}
	if got := status("self"); got != "0" {
		t.Fatalf("THP_enabled of the caller = %q after DisableTransparentHugePages, want 0", got)
	}
	out, err := exec.CommandContext(t.Context(), "grep", "THP_enabled", "/proc/self/status").Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(out)), "THP_enabled:")); got != "0" {
		t.Fatalf("THP_enabled of a child = %q, want 0", got)
	}
}

func TestDescendantFindsAGrandchildByName(t *testing.T) {
	named := filepath.Join(t.TempDir(), "perfgate-probe")
	// The probe is a shell under another name; it prints only after it has exec'd under that name, then waits in a child sleep.
	shellPath, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shellPath, named); err != nil {
		t.Fatal(err)
	}
	shell := exec.CommandContext(t.Context(), "sh", "-c", named+" -c 'echo started; sleep 30' & wait")
	shell.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-shell.Process.Pid, syscall.SIGKILL) // The shell, the probe and its sleep share the group.
		_ = shell.Wait()
	})
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	found := Descendant(shell.Process.Pid, "perfgate-probe")
	if found == 0 || found == shell.Process.Pid {
		t.Fatalf("Descendant(%d) = %d", shell.Process.Pid, found)
	}
	if Descendant(shell.Process.Pid, "no-such-process") != 0 {
		t.Fatal("found a process that does not exist")
	}
}
