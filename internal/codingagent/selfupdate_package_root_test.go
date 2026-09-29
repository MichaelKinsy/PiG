package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// Ports packages/coding-agent/test/config.test.ts:442. Pi checks package-directory and parent writability, not only the executable's immediate directory.
func TestNativeSelfUpdateRejectsReadOnlyPackageRoot(t *testing.T) {
	if testenv.RunUnprivileged(t) {
		return
	}
	t.Setenv("PIG_INSTALL_TIER", "")
	root := filepath.Join(t.TempDir(), "lib", "node_modules")
	pkg := filepath.Join(root, "@earendil-works", "pi-coding-agent")
	bin := filepath.Join(pkg, "dist")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := writeFakeExe(t, bin)
	testenv.ReadOnlyDir(t, pkg)
	provenance, err := resolveTierForExe(t, exe, fakeCmdRunner{outputs: map[string]string{"npm root -g": root}})
	if err != nil {
		t.Fatal(err)
	}
	if provenance.Tier != TierUnsupported {
		t.Fatalf("read-only package root is updateable: %+v", provenance)
	}
}
