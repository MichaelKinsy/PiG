package tools

import (
	"errors"
	"io/fs"
	"os/exec"
	"testing"
)

// Windows reports a missing shell from os/exec's executable lookup as
// exec.ErrNotFound, not an errno. Node's spawn reports it as ENOENT on every
// platform (.upstream/v0.87.1/packages/coding-agent/test/tools.test.ts:596).
func TestShellSpawnErrorReportsAMissingExecutableAsENOENT(t *testing.T) {
	const path = "/nonexistent-shell-path-xyz123"
	err := &shellSpawnError{path: path, cause: &exec.Error{Name: path, Err: exec.ErrNotFound}}
	if got, want := err.Error(), "spawn "+path+" ENOENT"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	if !errors.Is(err, fs.ErrNotExist) || !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("errors.Is(%v, fs.ErrNotExist/exec.ErrNotFound) = false", err)
	}
	other := &shellSpawnError{path: path, cause: errors.New("boom")}
	if other.Error() != "boom" || errors.Is(other, fs.ErrNotExist) {
		t.Fatalf("unrelated spawn error = %q, not-exist %t", other.Error(), errors.Is(other, fs.ErrNotExist))
	}
}
