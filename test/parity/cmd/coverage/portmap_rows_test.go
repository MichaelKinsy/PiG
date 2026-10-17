// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// portmap-check -fix-cells writes a Pig cell as several comma-separated code spans or leaves it empty. Every such row must stay an entry, because unit evidence and coverage look the upstream path up in this list.
func TestParsePortMapKeepsRowsWithSeveralPigFileSpans(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PORT_MAP.md")
	rows := "| Upstream | Pig | Status |\n" +
		"|---|---|---|\n" +
		"| `packages/tui/src/components/text.ts` | `tui/text.go` | ✅ |\n" +
		"| `packages/tui/src/components/truncated-text.ts` | `tui/truncated_text.go (D66 clamps)`, `tui/tui.go` | ✅ |\n" +
		"| `packages/a/b.ts` | `x/a.go`,`x/b.go`, `x/c.go` | 🟡 |\n" +
		"| `packages/a/c.ts` | `(designed out)` | n/a |\n" +
		"| `packages/codemode/src/wasm.ts` |  | ✅ |\n" +
		"| `packages/codemode/src/narrow.ts` | | 🟡 |\n"
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := parsePortMap(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []portMapEntry{
		{UpstreamPath: "packages/tui/src/components/text.ts", PigPath: "tui/text.go", Status: "✅", LineNum: 3},
		{UpstreamPath: "packages/tui/src/components/truncated-text.ts", PigPath: "tui/truncated_text.go (D66 clamps)`, `tui/tui.go", Status: "✅", LineNum: 4},
		{UpstreamPath: "packages/a/b.ts", PigPath: "x/a.go`,`x/b.go`, `x/c.go", Status: "🟡", LineNum: 5},
		{UpstreamPath: "packages/a/c.ts", PigPath: "(designed out)", Status: "n/a", LineNum: 6},
		{UpstreamPath: "packages/codemode/src/wasm.ts", PigPath: "", Status: "✅", LineNum: 7},
		{UpstreamPath: "packages/codemode/src/narrow.ts", PigPath: "", Status: "🟡", LineNum: 8},
	}
	if len(entries) != len(want) {
		t.Fatalf("parsed %d rows, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

// A Pig cell may carry prose after its code spans, with more code spans inside the prose. Skipping such a row drops its upstream path from every list built from PORT_MAP entries.
func TestParsePortMapKeepsRowsWhosePigCellCarriesProse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "PORT_MAP.md")
	rows := "| Upstream | PiG | Status |\n" +
		"|---|---|---|\n" +
		"| `packages/mcp/src/transports/transport.ts` | `mcp/transport.go` (missing at Pi 1.2.0: the argument of `McpTransport.send`, interface gap `McpTransport::property:send::call:0` signature-mismatch) | 🟡 |\n" +
		"| `packages/mcp/src/transports/streamable-http.ts` | `mcp/streamable_http.go; mcp/sse.go` (missing: `send(message, options?)`'s options) | 🟡 |\n" +
		"| `packages/a/b.ts` | `x/a.go` | ✅ |\n"
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := parsePortMap(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []portMapEntry{
		{UpstreamPath: "packages/mcp/src/transports/transport.ts", PigPath: "`mcp/transport.go` (missing at Pi 1.2.0: the argument of `McpTransport.send`, interface gap `McpTransport::property:send::call:0` signature-mismatch)", Status: "🟡", LineNum: 3},
		{UpstreamPath: "packages/mcp/src/transports/streamable-http.ts", PigPath: "`mcp/streamable_http.go; mcp/sse.go` (missing: `send(message, options?)`'s options)", Status: "🟡", LineNum: 4},
		{UpstreamPath: "packages/a/b.ts", PigPath: "x/a.go", Status: "✅", LineNum: 5},
	}
	if len(entries) != len(want) {
		t.Fatalf("parsed %d rows, want %d: %+v", len(entries), len(want), entries)
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, entries[i], want[i])
		}
	}
}

// Downstream rows map no upstream file and are not entries; every other table row that does not parse is an error naming its line.
func TestParsePortMapIgnoresDownstreamRowsAndRejectsMalformedRows(t *testing.T) {
	dir := t.TempDir()
	good := "| upstream | pig | status |\n|---|---|---|\n| _(downstream: Go SDK bridge)_ | `extensions/sdk/...` | 🔀 D19 |\n| `packages/a/b.ts` | `x/a.go` | ✅ |\n"
	path := filepath.Join(dir, "good.md")
	if err := os.WriteFile(path, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := parsePortMap(path)
	if err != nil || len(entries) != 1 || entries[0].UpstreamPath != "packages/a/b.ts" {
		t.Fatalf("entries = %+v, err = %v; want only packages/a/b.ts", entries, err)
	}
	for name, row := range map[string]string{
		"unknown status":   "| `packages/a/c.ts` | `x/c.go` | maybe |",
		"missing status":   "| `packages/a/c.ts` | `x/c.go` |",
		"no upstream span": "| packages/a/c.ts | `x/c.go` | ✅ |",
	} {
		bad := filepath.Join(dir, "bad.md")
		if err := os.WriteFile(bad, []byte("| upstream | pig | status |\n|---|---|---|\n| `packages/a/b.ts` | `x/a.go` | ✅ |\n"+row+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := parsePortMap(bad); err == nil || !strings.Contains(err.Error(), "bad.md:4:") {
			t.Errorf("%s: err = %v, want a malformed-row error naming line 4", name, err)
		}
	}
}
