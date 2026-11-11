//go:build !windows

package daemon

// pi: packages/env/daemon/src/process.rs

// Ports the test of packages/env/daemon/src/sys/unix.rs "ending_a_group_kills_what_the_program_left_running" (Pi 032656b6c).
// mutation-checked: dropping group.end from the test body leaves the sleeper running and fails it.

import (
	"io"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestEndingAGroupKillsWhatTheProgramLeftRunning(t *testing.T) {
	// A wrapper that starts a background process in its group, prints its pid and exits, as `npx` can.
	started, failure := startProgram(&processRequest{
		argv:       []string{"/bin/sh", "-c", "sleep 30 </dev/null >/dev/null 2>&1 & echo $!"},
		cwd:        t.TempDir(),
		env:        map[string]string{},
		inheritEnv: true,
	})
	if failure != nil {
		t.Fatal(failure.toJSON())
	}
	t.Cleanup(started.closePipes)
	output, err := io.ReadAll(started.stdout)
	if err != nil {
		t.Fatal(err)
	}
	sleeper, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		t.Fatalf("the wrapper printed %q: %v", output, err)
	}
	if err := started.group.waitForExit(); err != nil {
		t.Fatal(err)
	}
	started.group.end()
	started.group.forget()
	if code, err := reapProgram(started.cmd); err != nil || code != 0 {
		t.Fatalf("the wrapper ended with %d, %v", code, err)
	}
	// The killed sleeper is an orphan; it is gone once init has reaped it.
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(sleeper, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(sleeper, 0) == nil {
		t.Fatalf("the sleeper %d still runs after the group ended", sleeper)
	}
}
