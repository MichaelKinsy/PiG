package harness

import (
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// The lock-order overlay rewrites sync.Mutex in every package of the module except internal/lockorder, so a package that lockorder itself imports
// (internal/goroutineid, whose fast path holds a sync.Mutex) must stay untouched too: rewritten, it would import lockorder, which imports it. go
// resolves that as an import cycle, and the default instrumented journey build failed before any journey could start.
func TestLockOverlayLeavesThePackagesLockorderImportsAlone(t *testing.T) {
	root := testenv.ModuleRoot(t)
	overlay, err := writeLockOverlay(root, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "list", "-deps", "-overlay", overlay, "./internal/lockorder")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go list -deps of internal/lockorder under the overlay: %v\n%s", err, out)
	}
}
