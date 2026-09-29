package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuditCovers pins the loose-link detector: a covers entry whose PORT_MAP Go
// target had zero coverage in the single-scenario piglet is reported LOOSE and
// counted; a covered target is "exercised"; a designed-out/no-go target and an
// unmapped covers entry are reported but never counted as loose. The check must
// be false-positive-free: only a provably unexecuted Go target fails.
func TestAuditCovers(t *testing.T) {
	dir := t.TempDir()
	// A pig Go file whose glob target must resolve on disk for goTargets to
	// return it. goTargets globs against root, so create the files under root.
	for _, f := range []string{"exercised.go", "loose.go"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	scenario := filepath.Join(dir, "s.toml")
	if err := os.WriteFile(scenario, []byte(`name = "demo"
covers = [
  "packages/a/hit.ts",
  "packages/a/miss.ts",
  "packages/a/designed-out.ts",
  "packages/a/unmapped.ts",
]
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rows := map[string]portMapRow{
		"packages/a/hit.ts":          {target: "exercised.go", disposition: "⬜"},
		"packages/a/miss.ts":         {target: "loose.go", disposition: "⬜"},
		"packages/a/designed-out.ts": {target: "(not needed: no equivalent)", disposition: "n/a"},
		// unmapped.ts intentionally absent from rows.
	}
	// exercised.go covered; loose.go NOT covered.
	covered := map[string]bool{"exercised.go": true}

	var buf bytes.Buffer
	loose, err := auditCovers(&buf, dir, scenario, rows, covered)
	if err != nil {
		t.Fatalf("auditCovers: %v", err)
	}
	if loose != 1 {
		t.Fatalf("loose = %d, want 1 (only miss.ts)", loose)
	}
	out := buf.String()
	cases := []struct {
		file, want string
	}{
		{"hit.ts", "exercised"},
		{"miss.ts", "LOOSE"},
		{"designed-out.ts", "no-go-target"},
		{"unmapped.ts", "unmapped"},
	}
	for _, c := range cases {
		line := lineContaining(out, c.file)
		if line == "" {
			t.Fatalf("no row for %s in:\n%s", c.file, out)
		}
		if !strings.Contains(line, c.want) {
			t.Errorf("%s: row %q missing %q", c.file, line, c.want)
		}
	}
}

func lineContaining(s, sub string) string {
	for ln := range strings.SplitSeq(s, "\n") {
		if strings.Contains(ln, sub) {
			return ln
		}
	}
	return ""
}
