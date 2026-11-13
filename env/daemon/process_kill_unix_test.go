//go:build !windows

package daemon

// pi: packages/env/daemon/src/process.rs

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// killTree is sys/unix.rs kill_tree: it kills the program's process group, or the process itself when no group has its pid. A program the daemon started is a
// session leader, so its pid is a group; this one is not, so only the fallback reaches it.
func TestKillTreeFallsBackToTheProcessWhenThereIsNoGroup(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = append(os.Environ(), helperEnv+"=sleeper")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	watchProcess(t, cmd.Process.Pid)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	killTree(cmd.Process.Pid)
	select {
	case <-waited:
	case <-time.After(groupWait):
		t.Fatal("killTree left a program that is no group leader running")
	}
}
