package ai

import (
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory and the agent-directory variables (testenv.ScopeTempDir), so a test, or a Pi oracle child it starts, never
// reads or writes the credentials and settings of the user running `go test`.
func TestMain(m *testing.M) {
	root, err := os.MkdirTemp("", "pig-ai-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create scoped test temp directory:", err)
		os.Exit(2)
	}
	if err := testenv.ScopeTempDir(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
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
