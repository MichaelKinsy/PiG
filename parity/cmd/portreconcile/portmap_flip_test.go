package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlipPortMap pins the mechanical PORT_MAP derivation: a ⬜ row whose mapped
// Go target exists and is test-covered flips to ✅; a designed-out note, a
// named-but-missing Go file, and an uncovered-but-present file all stay ⬜ and
// are reported in their own bucket; 🟡 and ✅ rows are never touched.
func TestFlipPortMap(t *testing.T) {
	root := t.TempDir()
	// Real Go files so goTargets' glob resolves them.
	for _, f := range []string{"covered.go", "uncovered.go", "partial.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pm := filepath.Join(root, "PORT_MAP.md")
	body := "| upstream | pig | status |\n" +
		"|---|---|---|\n" +
		"| `a/hit.ts` | `covered.go` | ⬜ |\n" +
		"| `a/cold.ts` | `uncovered.go` | ⬜ |\n" +
		"| `a/gone.ts` | `absent.go` | ⬜ |\n" +
		"| `a/note.ts` | `(not needed: no equivalent)` | ⬜ |\n" +
		"| `a/part.ts` | `partial.go` | 🟡 |\n" +
		"| `a/done.ts` | `covered.go` | ✅ |\n"
	if err := os.WriteFile(pm, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	unitCov := map[string]bool{"covered.go": true}
	parityCov := map[string]bool{}

	var buf bytes.Buffer
	if err := flipPortMap(&buf, pm, root, unitCov, parityCov, true); err != nil {
		t.Fatalf("flipPortMap: %v", err)
	}
	got, _ := os.ReadFile(pm)
	out := string(got)

	// Only the covered ⬜ row flips.
	assertLine(t, out, "a/hit.ts", "| ✅ |")
	assertLine(t, out, "a/cold.ts", "| ⬜ |") // present but uncovered
	assertLine(t, out, "a/gone.ts", "| ⬜ |") // missing file
	assertLine(t, out, "a/note.ts", "| ⬜ |") // designed-out note
	assertLine(t, out, "a/part.ts", "| 🟡 |") // 🟡 untouched
	assertLine(t, out, "a/done.ts", "| ✅ |") // already ✅ untouched

	report := buf.String()
	for _, want := range []string{
		"flipped ⬜ → ✅ (mapped Go target exists and is test-covered): 1",
		"designed-out note, no Go target: 1",
		"mapped Go file MISSING (genuine gap to pi): 1",
		"Go file exists but UNCOVERED by our tests: 1",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q in:\n%s", want, report)
		}
	}
}

func assertLine(t *testing.T, out, key, wantStatus string) {
	t.Helper()
	for ln := range strings.SplitSeq(out, "\n") {
		if strings.Contains(ln, key) {
			if !strings.Contains(ln, wantStatus) {
				t.Errorf("%s: line %q missing status %q", key, ln, wantStatus)
			}
			return
		}
	}
	t.Errorf("no line for %s", key)
}
