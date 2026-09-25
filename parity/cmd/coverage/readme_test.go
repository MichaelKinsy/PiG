package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readmeEntries() []portMapEntry {
	return []portMapEntry{
		{UpstreamPath: "packages/tui/src/tui.ts", Status: "✅"},
		{UpstreamPath: "packages/agent/src/agent.ts", Status: "✅"},
		{UpstreamPath: "packages/ai/src/stream.ts", Status: "🟡"},
		{UpstreamPath: "packages/ai/src/index.ts", Status: "n/a"},
		{UpstreamPath: "packages/coding-agent/src/main.ts", Status: "⏸"},
		{UpstreamPath: "packages/agent/src/harness/new.ts", PigPath: "(new upstream by 9.9.9; not mapped)", Status: "⬜"},
		{UpstreamPath: "packages/agent/src/harness/old.ts", PigPath: "(new upstream by 9.9.8; not mapped)", Status: "⬜"},
	}
}

func TestPortingBlockExplainsTheDenominator(t *testing.T) {
	entries := readmeEntries()
	stats := computeStatusStats(entries, nil, nil)
	stats.Behavioral = 1
	block := portingBlock(entries, stats, "9.9.9")
	for _, want := range []string{
		"**Porting:** 2 of 5 intended-portable upstream files are ported (40.0%).",
		"rows for Pi's `agent`, `ai`, `coding-agent`, and `tui` packages, excluding 1 `n/a` and 1 deferred rows.",
		"Of the other 3, 1 are partial and 2 are not started.",
		"Not started includes 1 files new in Pi 9.9.9",
		"**Behavioral evidence badge:** 1 of the 2 ported files (50.0%)",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("porting block lacks %q:\n%s", want, block)
		}
	}
}

func TestPortingBlockNamesAllNewRowsWhenEveryNotStartedRowIsNew(t *testing.T) {
	entries := readmeEntries()[:6]
	block := portingBlock(entries, computeStatusStats(entries, nil, nil), "9.9.9")
	if !strings.Contains(block, "The not-started rows are files new in Pi 9.9.9 that PiG has not ported yet.") {
		t.Fatalf("porting block does not attribute the not-started rows to the pin:\n%s", block)
	}
}

func TestPortingBlockMatchesSummaryLine(t *testing.T) {
	entries := readmeEntries()
	stats := computeStatusStats(entries, nil, nil)
	summary := stats.summaryLine()
	block := portingBlock(entries, stats, "9.9.9")
	if !strings.Contains(summary, "2 / 5 intended-portable entries ✅ (40.0%)") || !strings.Contains(block, "2 of 5 intended-portable upstream files are ported (40.0%)") {
		t.Fatalf("README and AGENTS.md numbers disagree:\nsummary: %s\nblock: %s", summary, block)
	}
}

func TestPatchFileBlockRewritesOnlyBetweenMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	original := "badges\n" + portingBeginMarker + "\nstale\n" + portingEndMarker + "\nrest\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := patchFileBlock(path, portingBeginMarker, portingEndMarker, "fresh"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "badges\n" + portingBeginMarker + "\nfresh\n" + portingEndMarker + "\nrest\n"
	if string(got) != want {
		t.Fatalf("patched README = %q, want %q", got, want)
	}
}

func TestPatchFileBlockRejectsMissingMarkers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "README.md")
	if err := os.WriteFile(path, []byte("no markers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := patchFileBlock(path, portingBeginMarker, portingEndMarker, "fresh"); err == nil || !strings.Contains(err.Error(), "markers") {
		t.Fatalf("missing markers error = %v", err)
	}
}
