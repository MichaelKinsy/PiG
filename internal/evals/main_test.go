package evals

import (
	"fmt"
	"os"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestMain scopes the temporary directory and the agent directories to one tree that it removes after the run. The pig binaries builtBinaries links and the pig-ext-*.log files of the pig processes the harness tests start stay inside that tree.
func TestMain(m *testing.M) {
	os.Exit(runScoped(m))
}

func runScoped(m *testing.M) int {
	root, err := os.MkdirTemp("", "pig-evals-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create scoped test temp directory:", err)
		return 2
	}
	// A root runner drops the agent to the sandbox identity, which must still traverse to the binaries; 0711 grants the traversal the shared temporary directory grants and no listing.
	if err := os.Chmod(root, 0o711); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		return 2
	}
	if err := testenv.ScopeTempDir(root); err != nil {
		fmt.Fprintln(os.Stderr, err)
		_ = os.RemoveAll(root)
		return 2
	}
	code := m.Run()
	if err := os.RemoveAll(root); err != nil {
		fmt.Fprintln(os.Stderr, "remove scoped test temp directory:", err)
		if code == 0 {
			code = 2
		}
	}
	return code
}
