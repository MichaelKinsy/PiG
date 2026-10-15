package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/test/parity/upstreampackages"
)

// writeMirror writes a package.json for every listed package. deps maps a package key to the keys it depends on, and files is the coding-agent files list.
func writeMirror(t *testing.T, deps map[string][]string, files []string) string {
	t.Helper()
	root := t.TempDir()
	names := map[string]string{}
	for _, p := range upstreampackages.All() {
		names[p.Key] = p.Name
	}
	for _, p := range upstreampackages.All() {
		manifest := map[string]any{"name": p.Name}
		dependencies := map[string]string{}
		for _, dep := range deps[p.Key] {
			dependencies[names[dep]] = "^1.0.0"
		}
		manifest["dependencies"] = dependencies
		if p.Key == "coding-agent" {
			manifest["files"] = files
		}
		body, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(root, "packages", p.Key)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestShippedSurfaceFollowsTheCodingAgentManifest(t *testing.T) {
	root := writeMirror(t, map[string][]string{
		"coding-agent": {"agent", "tui"},
		"agent":        {"ai"},
		"ai":           {"telemetry"},
		"client":       {"protocol"},
	}, []string{"dist", "!dist/client", "!dist/cli/experimental", "docs"})
	packages, excluded, err := shippedSurface(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"agent", "ai", "coding-agent", "telemetry", "tui"}; !slices.Equal(packages, want) {
		t.Errorf("shipped packages = %v, want %v", packages, want)
	}
	if want := []string{"packages/coding-agent/src/cli/experimental/", "packages/coding-agent/src/client/"}; !slices.Equal(excluded, want) {
		t.Errorf("excluded prefixes = %v, want %v", excluded, want)
	}
}

func TestAccountPackagesSplitsSubtotals(t *testing.T) {
	root := writeMirror(t, map[string][]string{"coding-agent": {"agent"}}, []string{"!dist/experimental"})
	entries := []portMapEntry{
		{UpstreamPath: "packages/agent/src/a.ts", Status: "✅"},
		{UpstreamPath: "packages/agent/src/b.ts", Status: "🟡"},
		{UpstreamPath: "packages/coding-agent/src/x.ts", Status: "✅"},
		{UpstreamPath: "packages/coding-agent/src/experimental/y.ts", Status: "✅"},
		{UpstreamPath: "packages/env/src/z.ts", Status: "⬜"},
		{UpstreamPath: "packages/tui/src/index.ts", Status: "n/a"},
	}
	accounting, err := accountPackages(entries, nil, nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := accounting.Whole; got.Total != 6 || got.Ported != 3 || got.NotStarted != 1 || got.NA != 1 {
		t.Errorf("whole = %+v", got)
	}
	if got := accounting.Core; got.Total != 5 || got.Ported != 3 {
		t.Errorf("core = %+v, want 5 rows, 3 ported (agent, coding-agent, tui)", got)
	}
	if got := accounting.Shipped; got.Total != 3 || got.Ported != 2 || got.Partial != 1 {
		t.Errorf("shipped = %+v, want agent plus the non-experimental coding-agent row", got)
	}
}

func TestAccountPackagesRejectsAPackageOutsideTheSharedList(t *testing.T) {
	root := writeMirror(t, nil, nil)
	_, err := accountPackages([]portMapEntry{{UpstreamPath: "packages/unlisted/src/a.ts", Status: "✅"}}, nil, nil, root)
	if err == nil || !strings.Contains(err.Error(), "unlisted") {
		t.Fatalf("error = %v, want a rejection naming the unlisted package", err)
	}
}

func TestInterfaceClosureCountsOnlyClosingDispositions(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "test", "parity", "interfaces")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"mappings":[{"disposition":"pending"},{"disposition":"deferred"},{"disposition":"deferred"},{"disposition":"designed-out"},{"disposition":"ported"}]}`
	if err := os.WriteFile(filepath.Join(dir, "mapping-v9.9.9.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	closure, err := loadInterfaceClosure(root, "9.9.9")
	if err != nil || closure == nil {
		t.Fatalf("loadInterfaceClosure = %v, %v", closure, err)
	}
	want := "- **Interface closure:** 2 of 5 semantic interface IDs closed (2 deferred, 1 designed-out, 1 pending, 1 ported). A file-level ✅ does not close an interface ID."
	if got := closure.line(); got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if missing, err := loadInterfaceClosure(root, "0.0.0"); missing != nil || err != nil {
		t.Errorf("a missing mapping must report no closure, got %v, %v", missing, err)
	}
}
