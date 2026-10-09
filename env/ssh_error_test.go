package env

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// upstream: packages/env/src/ssh.ts SshError(message, exitCode, stderr): a failed ssh reports its exit code and stderr, and the message names both.
// Pi source: packages/env/src/ssh.ts:46-54 (SshError).
// mutation-checked: the mutants "NewSshError drops the exit code" and "SshError.Error is empty" fail it.
func TestSshErrorReportsExitCodeMessageAndStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("scripted ssh is a POSIX shell script")
	}
	program := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(program, []byte("#!/bin/sh\necho boom >&2\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	target := SshTarget{Host: "h", Ssh: &program, KnownHostsFile: filepath.Join(t.TempDir(), "known_hosts"), HostKeyAlias: "pi-env-test"}
	_, err := runSsh(context.Background(), target, "true", sshRun{})
	var sshErr *SshError
	if !errors.As(err, &sshErr) {
		t.Fatalf("err = %v, want *SshError", err)
	}
	if sshErr.ExitCode == nil || *sshErr.ExitCode != 3 {
		t.Fatalf("ExitCode = %v, want 3", sshErr.ExitCode)
	}
	if sshErr.Stderr != "boom\n" || sshErr.Message != "ssh h failed with exit code 3: boom" || sshErr.Error() != sshErr.Message {
		t.Fatalf("SshError = %+v", sshErr)
	}
}

// upstream: the constructor stores its three arguments; a nil exit code is upstream's null.
// mutation-checked: dropping the reads and writes of SshError.ExitCode, SshError.Message fails it
// Pi: packages/env/src/ssh.ts:50 (message)
// Pi source: packages/env/src/ssh.ts:50-53 (SshError constructor).
// mutation-checked: the mutant "NewSshError drops the exit code" fails it.
func TestNewSshErrorKeepsItsArguments(t *testing.T) {
	code := 7
	got := NewSshError("m", &code, "e")
	if got.Message != "m" || got.ExitCode == nil || *got.ExitCode != 7 || got.Stderr != "e" {
		t.Fatalf("NewSshError = %+v", got)
	}
	if NewSshError("m", nil, "").ExitCode != nil {
		t.Fatal("a nil exit code must stay nil")
	}
}
