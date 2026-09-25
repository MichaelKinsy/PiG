package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// portMapDriftFixture writes a minimal repo: one live upstream file, a pinned
// version, and a PORT_MAP whose extra rows name files upstream no longer has.
func portMapDriftFixture(t *testing.T, rows string) (script, root string) {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	script = filepath.Join(filepath.Dir(thisFile), "..", "..", "automation", "ci", "check-port-map-drift.py")
	root = t.TempDir()
	files := map[string]string{
		filepath.Join(".upstream", "current", "packages", "ai", "src", "live.ts"): "export const live = 1;\n",
		filepath.Join("coding", "pigversion", "pigversion.go"):                    "package pigversion\nconst UpstreamVersion = \"9.9.9\"\n",
		"PORT_MAP.md": "# PORT_MAP\n\n## `packages/ai/src/`\n\n| upstream | pig | status |\n|---|---|---|\n" +
			"| `packages/ai/src/live.ts` | `ai/live.go` | ✅ |\n" + rows,
	}
	for rel, body := range files {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return script, root
}

func runPortMapDrift(t *testing.T, script, root string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{script, "--upstream", filepath.Join(root, ".upstream", "current"), "--port-map", filepath.Join(root, "PORT_MAP.md")}, extra...)
	output, err := exec.Command("python3", args...).CombinedOutput()
	return string(output), err
}

// A removed upstream file has nothing to port, defer, or rule out, so the gate
// rejects its row under every status, not only the live ones.
func TestPortMapDriftRejectsRowsForRemovedUpstreamFilesUnderAnyStatus(t *testing.T) {
	for _, status := range []string{"✅", "🟡", "⬜", "🔴", "n/a", "⏸"} {
		t.Run(status, func(t *testing.T) {
			script, root := portMapDriftFixture(t, "| `packages/ai/src/gone.ts` | `(removed upstream)` | "+status+" |\n")
			output, err := runPortMapDrift(t, script, root)
			if err == nil {
				t.Fatalf("row for a removed upstream file passed with status %s:\n%s", status, output)
			}
			if !strings.Contains(output, "packages/ai/src/gone.ts ["+status+"]") {
				t.Fatalf("output does not name the stale row:\n%s", output)
			}
		})
	}
}

func TestPortMapDriftAcceptsRowsForExistingFilesOnly(t *testing.T) {
	script, root := portMapDriftFixture(t, "")
	output, err := runPortMapDrift(t, script, root)
	if err != nil || !strings.Contains(output, "port-map-drift: clean (1 upstream source files accounted for)") {
		t.Fatalf("clean map rejected: %v\n%s", err, output)
	}
}

func TestPortMapDriftReconcileDeletesRowsForRemovedFiles(t *testing.T) {
	script, root := portMapDriftFixture(t, "| `packages/ai/src/gone.ts` | `ai/gone.go` | ✅ |\n| `packages/ai/src/old.ts` | `(Node-only)` | n/a |\n")
	output, err := runPortMapDrift(t, script, root, "--reconcile")
	if err != nil {
		t.Fatalf("reconcile failed: %v\n%s", err, output)
	}
	data, err := os.ReadFile(filepath.Join(root, "PORT_MAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "gone.ts") || strings.Contains(string(data), "old.ts") {
		t.Fatalf("reconcile kept rows for removed files:\n%s", data)
	}
	if !strings.Contains(string(data), "| `packages/ai/src/live.ts` | `ai/live.go` | ✅ |") {
		t.Fatalf("reconcile dropped the live row:\n%s", data)
	}
}

// Explicit implementation claims must not coexist with an unstarted or
// designed-out ledger row. A partial port is deliberately not promoted to done.
func TestPortMapDriftRejectsUnreconciledImplementationClaims(t *testing.T) {
	for _, status := range []string{"⬜", "n/a", "⏸", "🟡", "✅"} {
		t.Run(status, func(t *testing.T) {
			script, root := portMapDriftFixture(t, "")
			path := filepath.Join(root, "PORT_MAP.md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "✅", status)), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(root, "ai"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "ai", "live.go"), []byte("package ai\n// This file ports packages/ai/src/live.ts.\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			output, err := runPortMapDrift(t, script, root, "--reconcile")
			blocked := status == "⬜" || status == "n/a" || status == "⏸"
			if blocked && (err == nil || !strings.Contains(output, "implementation claim contradicts")) {
				t.Fatalf("stale implementation status accepted: %v\n%s", err, output)
			}
			if !blocked && err != nil {
				t.Fatalf("reviewed mapping rejected: %v\n%s", err, output)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != strings.ReplaceAll(string(data), "✅", status) {
				t.Fatal("reconcile promoted status without review")
			}
		})
	}
}

func TestPortMapDriftIgnoresTestAndIncidentalSourceReferences(t *testing.T) {
	script, root := portMapDriftFixture(t, "")
	path := filepath.Join(root, "PORT_MAP.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "✅", "⬜")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "ai"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"live_test.go": "package ai\n// Ports packages/ai/src/live.ts.\n",
		"reference.go": "package ai\n// See packages/ai/src/live.ts; not implemented here.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "ai", name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	output, err := runPortMapDrift(t, script, root)
	if err != nil {
		t.Fatalf("incidental reference treated as implementation: %v\n%s", err, output)
	}
}
