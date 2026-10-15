//go:build !unix

package evals

// The eval filesystem sandbox needs POSIX user APIs. harness_run_test.go (unix only) covers the sandbox itself; on
// other platforms the package still builds and refuses a sandbox identity with the documented error instead of running
// the agent unsandboxed.

import (
	"os/exec"
	"testing"
)

func TestSandboxRequiresPOSIXUserAPIsOffUnix(t *testing.T) {
	const want = "The eval filesystem sandbox requires POSIX user APIs."
	if err := enterToolSandbox(exec.Command("true"), t.TempDir(), &sandboxIdentity{uid: 65532, gid: 65532}); err == nil || err.Error() != want {
		t.Fatalf("enterToolSandbox with an identity = %v, want %q", err, want)
	}
	if err := enterToolSandbox(exec.Command("true"), t.TempDir(), nil); err != nil {
		t.Fatalf("enterToolSandbox without an identity = %v, want nil", err)
	}
	if err := chownTree(t.TempDir(), sandboxIdentity{uid: 1, gid: 1}); err == nil || err.Error() != want {
		t.Fatalf("chownTree = %v, want %q", err, want)
	}
}
