package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortMapDriftKeepsExplicitLiveGeneratorSources(t *testing.T) {
	for _, reconcile := range []bool{false, true} {
		t.Run(map[bool]string{false: "check", true: "reconcile"}[reconcile], func(t *testing.T) {
			const source = "packages/ai/scripts/reasoning-options.ts"
			script, root := portMapDriftFixture(t, "| `"+source+"` | `internal/modelgen/reasoning.go` | 🟡 |\n")
			path := filepath.Join(root, ".upstream", "current", filepath.FromSlash(source))
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("export const reasoning = true;\n"), 0644); err != nil {
				t.Fatal(err)
			}
			args := []string{}
			if reconcile {
				args = append(args, "--reconcile")
			}
			output, err := runPortMapDrift(t, script, root, args...)
			if err != nil {
				t.Fatalf("live mapped generator source was treated as deleted: %v\n%s", err, output)
			}
			document, err := os.ReadFile(filepath.Join(root, "docs/parity/PORT_MAP.md"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(document), source) {
				t.Fatal("reconcile deleted a live mapped generator")
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			output, err = runPortMapDrift(t, script, root)
			if err == nil || !strings.Contains(output, source) {
				t.Fatalf("actually removed generator was not rejected: %v\n%s", err, output)
			}
		})
	}
}
