package main

import (
	"bytes"
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
		"| `a/prose.ts` | `x/p.go` (missing: `send(message)`, interface gap `T::property:send`) | 🟡 |\n"
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := parsePortMap(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]portMapRow{
		"a/one.ts":   {target: "x/one.go", disposition: "✅"},
		"a/many.ts":  {target: "x/a.go, x/b.go", disposition: "✅"},
		"a/prose.ts": {target: "x/p.go (missing: send(message), interface gap T::property:send)", disposition: "🟡"},
	}
	if len(got) != len(want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %+v, want %+v", k, got[k], w)
		}
	}
	bad := filepath.Join(dir, "bad.md")
	if err := os.WriteFile(bad, []byte(rows+"| `a/x.ts` | `x/x.go` | maybe |\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parsePortMap(bad); err == nil || !strings.Contains(err.Error(), "bad.md:7:") {
		t.Errorf("err = %v, want a malformed-row error naming line 7", err)
	}
}

// A ⬜ row whose Pig cell lists several code spans is a row too: it flips when a target is covered.
func TestFlipPortMapFlipsARowWithSeveralPigSpans(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pm := filepath.Join(root, "PORT_MAP.md")
	if err := os.WriteFile(pm, []byte("| upstream | pig | status |\n|---|---|---|\n| `a/m.ts` | `a.go`, `b.go` | ⬜ |\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := flipPortMap(&buf, pm, root, map[string]bool{"a.go": true}, map[string]bool{}, true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(pm)
	if !strings.Contains(string(got), "`a.go`, `b.go` | ✅ |") {
		t.Errorf("PORT_MAP after flip:\n%s", got)
	}
}
