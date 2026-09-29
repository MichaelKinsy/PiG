package main

import (
	"bytes"
	"strings"
	"testing"
)

// Committed coverage and explicit stdout/file reports must have one stable order even when multiple scenarios reference paths absent from PORT_MAP.
func TestReportSortsOrphanCoversByScenarioAndPath(t *testing.T) {
	scenarios := []scenarioFile{
		{Name: "zeta", Covers: []string{"packages/z.ts", "packages/a.ts"}},
		{Name: "alpha", Covers: []string{"packages/y.ts", "packages/b.ts", "packages/known.ts"}},
	}
	entries := []portMapEntry{{UpstreamPath: "packages/known.ts", Status: "🟡"}}
	var report bytes.Buffer
	emitReport(&report, entries, nil, nil, nil, scenarios, testPortingStats{})
	_, got, ok := strings.Cut(report.String(), "## Orphan covers (scenario references no PORT_MAP entry)\n\n")
	if !ok {
		t.Fatal("missing orphan-cover section")
	}
	want := "- `packages/b.ts` (scenario: alpha)\n" +
		"- `packages/y.ts` (scenario: alpha)\n" +
		"- `packages/a.ts` (scenario: zeta)\n" +
		"- `packages/z.ts` (scenario: zeta)\n"
	if got != want {
		t.Fatalf("orphan-cover ordering:\ngot:\n%swant:\n%s", got, want)
	}
}
