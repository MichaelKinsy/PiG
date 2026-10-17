package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePortMapPathsKeepsEveryPigCellShapeAndRejectsMalformedRows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "PORT_MAP.md")
	rows := "| Upstream | PiG | Status |\n|---|---|---|\n" +
		"| _(downstream: Go SDK bridge)_ | `extensions/sdk/...` | 🔀 D19 |\n" +
		"| `a/one.ts` | `x/one.go` | ✅ |\n" +
		"| `a/many.ts` | `x/a.go`, `x/b.go` | ✅ |\n" +
		"| `a/empty.ts` |  | 🟡 |\n" +
		"| `a/prose.ts` | `x/p.go` (missing: `send(message)`, interface gap `T::property:send`) | 🟡 |\n"
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parsePortMapPaths(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("paths = %v, want the four upstream rows", got)
	}
	for _, p := range []string{"a/one.ts", "a/many.ts", "a/empty.ts", "a/prose.ts"} {
		if !got[p] {
			t.Errorf("%s missing from %v", p, got)
		}
	}
	for name, row := range map[string]string{
		"unknown status": "| `a/x.ts` | `x/x.go` | maybe |",
		"missing status": "| `a/x.ts` | `x/x.go` |",
		"no code span":   "| a/x.ts | `x/x.go` | ✅ |",
	} {
		bad := filepath.Join(dir, "bad.md")
		if err := os.WriteFile(bad, []byte(rows+row+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := parsePortMapPaths(bad); err == nil || !strings.Contains(err.Error(), "bad.md:8:") {
			t.Errorf("%s: err = %v, want a malformed-row error naming line 8", name, err)
		}
	}
}
