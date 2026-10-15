// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestProcessMemoryReadsThisProcess(t *testing.T) {
	rss, pss := ProcessMemory(t, os.Getpid())
	if rss <= 0 || pss <= 0 || pss > rss {
		t.Fatalf("rss %v pss %v", rss, pss)
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
