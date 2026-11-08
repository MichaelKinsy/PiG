package node

// Ports packages/durable/test/env-node.test.ts it("ignores asynchronous taskkill spawn errors during abort") in PiG's own form.
// Pi points SystemRoot at a missing directory and fakes process.platform so the abort's taskkill spawn fails, kills the shell by
// hand, and expects an aborted result. Go's Start returns that failure synchronously, so the seam killProcessTree (taskkill on
// Windows) returns it: the test makes the kill fail the same way, kills the shell by hand, and expects the same aborted result,
// on every platform.

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
)

func TestShellIgnoresAKillThatFailsToSpawnDuringAbort(t *testing.T) {
	env, root := newTestEnv(t)
	pidFile := filepath.Join(root, "shell.pid")
	killed := make(chan struct{}, 1)
	previous := killProcessTree
	killProcessTree = func(int) error {
		killed <- struct{}{}
		return errors.New("taskkill: spawn failed")
	}
	t.Cleanup(func() { killProcessTree = previous })

	ctx, cancel := cancellable()
	done := make(chan error, 1)
	go func() {
		_, err := env.Exec(ctx, "echo $$ > '"+pidFile+"'; exec sleep 60", nil)
		done <- err
	}()
	var pid int
	for deadline := time.Now().Add(5 * time.Second); pid == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the shell never wrote its pid")
		}
		if data, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		}
	}
	cancel()
	select {
	case <-killed:
	case <-time.After(5 * time.Second):
		t.Fatal("the abort did not try to kill the process tree")
	}
	// The kill failed, so the test ends the shell itself, as Pi's does with SIGKILL.
	process, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		_ = execErr(t, err, durableenv.ExecutionErrorAborted)
	case <-time.After(10 * time.Second):
		t.Fatal("the aborted exec did not settle")
	}
}
