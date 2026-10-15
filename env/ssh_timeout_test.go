package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// upstream: packages/env/src/ssh.ts:136-146 runSsh's timer calls child.kill(), Node's SIGTERM, and rejects at once with the
// stderr read so far; it does not wait for the child's close, which a descendant holding stdout or stderr open delays.
// mutation-checked: the mutants "the timeout kills with SIGKILL" and "runSsh waits for the child at its timeout" fail it.
func TestRunSshTimeoutTerminatesTheChildAndFailsWithoutWaitingForItsOutputToClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripted ssh is a POSIX shell script")
	}
	directory := t.TempDir()
	marker := filepath.Join(directory, "terminated")
	program := filepath.Join(directory, "ssh")
	// The backgrounded sleep inherits stdout and stderr and outlives the script, so the output stays open after the
	// script exits; the trap records which signal ended the script.
	script := "#!/bin/sh\necho partial >&2\ntrap 'echo TERM > " + marker + "; exit 143' TERM\nsleep 8 &\nwait\n"
	writeProgram(t, program, script)
	target := SshTarget{Host: "h", Ssh: &program}
	timeout := 300 * time.Millisecond
	started := time.Now()
	_, err := runSsh(context.Background(), target, "true", sshRun{args: []string{}, timeout: timeout})
	took := time.Since(started)
	var sshErr *SshError
	if !errors.As(err, &sshErr) {
		t.Fatalf("err = %v, want *SshError", err)
	}
	if sshErr.Message != "ssh h did not finish within 0.3 s: partial" || sshErr.ExitCode != nil || sshErr.Stderr != "partial\n" {
		t.Fatalf("SshError = %+v", sshErr)
	}
	if took > timeout+2*time.Second {
		t.Fatalf("runSsh returned %s after its %s timeout; it must fail when the timer fires", took-timeout, timeout)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		content, readErr := os.ReadFile(marker)
		if readErr == nil && strings.TrimSpace(string(content)) == "TERM" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the child did not receive SIGTERM (marker %q, %v)", content, readErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
