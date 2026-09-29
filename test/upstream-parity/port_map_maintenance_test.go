package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortMapDriftRetainsExistingMaintenanceScripts(t *testing.T) {
	const relative = "packages/ai/scripts/generate-image-models.ts"
	script, root := portMapDriftFixture(t, "| `"+relative+"` | `cmd/gen-image-models/openrouter.go` | 🟡 |\n")
	source := filepath.Join(root, ".upstream", "current", filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("export function parse() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{nil, {"--reconcile"}} {
		if output, err := runPortMapDrift(t, script, root, extra...); err != nil {
			t.Fatalf("existing maintenance script classified as deleted: %v\n%s", err, output)
		}
		data, err := os.ReadFile(filepath.Join(root, "docs/parity/PORT_MAP.md"))
		if err != nil || !strings.Contains(string(data), relative) {
			t.Fatalf("reconcile removed an existing script: %v\n%s", err, data)
		}
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if output, err := runPortMapDrift(t, script, root); err == nil || !strings.Contains(output, relative) {
		t.Fatalf("removed script was not rejected: %v\n%s", err, output)
	}
}
