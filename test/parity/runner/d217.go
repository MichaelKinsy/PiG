//go:build parity

package runner

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// d217HookDir is the directory of the D217 Node module hook: the pinned Pi's modules plus the Pi fixes 0.6.0 carries (tui/testdata/pi-d217/README.md).
const d217HookDir = "tui/testdata/pi-d217"

// d217Hook returns the file URL of the hook's register.mjs while its manifest swaps at least one module, and "" once the manifest is empty or the
// directory is gone, when the pinned Pi's own modules are the carried ones.
func d217Hook(root string) (string, error) {
	dir := filepath.Join(root, filepath.FromSlash(d217HookDir))
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var modules []json.RawMessage
	if err := json.Unmarshal(data, &modules); err != nil {
		return "", fmt.Errorf("%s/manifest.json: %w", d217HookDir, err)
	}
	if len(modules) == 0 {
		return "", nil
	}
	return fileURL(filepath.Join(dir, "register.mjs")), nil
}

// fileURL returns the file: URL of an absolute path, the form Node's --import accepts on every platform.
func fileURL(path string) string {
	slashed := filepath.ToSlash(path)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

// d217Launch makes the pi side load the D217 hook. The pin's bundled bin (dist/bundle/cli.js) inlines pi-tui and the other modules the hook swaps, so
// while the hook swaps a module the runner starts the package's unbundled entry (dist/cli.js) under Node with the hook imported first, for every
// scenario that runs the resolved pi; the base and scenario arguments follow as for the bundle. A scenario that runs its own program (pi_bin) is not
// launched through it: an oracle script of such a scenario imports the hook itself (README.md). With an empty manifest the bundle runs unchanged.
func d217Launch(t *testing.T, root string, pi BinaryRef) BinaryRef {
	t.Helper()
	hook, err := d217Hook(root)
	if err != nil {
		t.Fatalf("parity: read the D217 hook manifest: %v", err)
	}
	if hook == "" {
		return pi
	}
	pkg, err := piPackageRoot(pi.Path)
	if err != nil {
		t.Fatalf("parity: locate the Pi package for the D217 hook: %v", err)
	}
	entry := filepath.Join(pkg, "dist", "cli.js")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("parity: the unbundled Pi entry the D217 hook needs: %v", err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("parity: the D217 hook needs node: %v", err)
	}
	pi.Launch = &Launch{Program: node, Args: []string{"--import", hook, entry}}
	return pi
}
