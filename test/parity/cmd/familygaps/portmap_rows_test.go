package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParsePortMapKeepsEveryPigCellShapeAndRejectsMalformedRows(t *testing.T) {
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
	entries, err := parsePortMap(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []portMapEntry{{"a/one.ts", "✅"}, {"a/many.ts", "✅"}, {"a/empty.ts", "🟡"}, {"a/prose.ts", "🟡"}}
	if len(entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v", entries, want)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(bad, []byte(rows+"| `a/x.ts` | `x/x.go` | maybe |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePortMap(bad); err == nil || !strings.Contains(err.Error(), "bad.md:8:") {
		t.Errorf("err = %v, want a malformed-row error naming line 8", err)
	}
}
