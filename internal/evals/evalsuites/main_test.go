package evalsuites

import (
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain keeps the pig processes that the footer oracle starts inside a scratch tree: ScopeTempDir isolates the agent directories as well as the temporary directory.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "pig-evalsuites-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := testenv.ScopeTempDir(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
