package cli

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/toolchain"
)

// requireSDKFixtureToolchain fails, not skips, when the interpreter an SDK's fixture runs on is missing: a skipped Lisp row would let a broken
// Lisp SDK pass on a machine without SBCL.
func requireSDKFixtureToolchain(t *testing.T, sdk string) {
	t.Helper()
	if sdk != "lisp" {
		return
	}
	if _, err := exec.LookPath(toolchain.SBCLExecutable(runtime.GOOS, exec.LookPath)); err != nil {
		t.Fatalf("sbcl is required for the Lisp SDK fixture: %v", err)
	}
}
