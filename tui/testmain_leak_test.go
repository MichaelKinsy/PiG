// SPDX-License-Identifier: MIT

package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory and the agent-directory variables (testenv.ScopeTempDir) and fails the package's test run when any test leaks a
// goroutine. The scope matters because TestUpstreamEditorCompletionIgnoresInvalidArgumentResult starts an extension host: a Host creates its per-user
// socket directory ($XDG_RUNTIME_DIR/pig, else $TMPDIR/pig-<uid>) and removes only its own runtime directory inside it, so without the scope that directory
// stays in the shared temporary directory after every run.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "pig-tui-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create scoped test temp directory:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		os.Exit(2)
	}
	// A Host puts its sockets below $XDG_RUNTIME_DIR/pig when the variable is set, which a Linux login session does: pin it to the scope too.
	if err := os.Setenv("XDG_RUNTIME_DIR", root); err != nil {
		fmt.Fprintln(os.Stderr, "scope XDG_RUNTIME_DIR:", err)
		_ = os.RemoveAll(root)
		os.Exit(2)
	}
	code := testenv.RunWithoutGoroutineLeaks(m)
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "remove scoped test temp directory:", err)
		if code == 0 {
			code = 2
		}
	}
	os.Exit(code)
}
