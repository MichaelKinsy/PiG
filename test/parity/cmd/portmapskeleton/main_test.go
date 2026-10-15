package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"docs/parity/PORT_MAP.md": "| `packages/fx/src/widget.ts` | `lib/widget.go` | ✅ |\n" +
			"| `packages/fx/src/group.ts` | `lib/{one,two}.go, ghost/missing.go` | ✅ |\n" +
			"| `packages/fx/src/nothing.ts` | `no go here` | ✅ |\n" +
			"| `packages/fx/src/skipped.ts` | `lib/widget.go` | 🟡 |\n",
		"lib/widget.go": "package lib\n\nfunc Widget() int { return 1 }\n\ntype Box struct{}\n\nfunc (*Box) Open() {}\n\nfunc hidden() {}\n",
		"lib/one.go":    "package lib\n\nfunc One() int { return 1 }\n",
		"lib/two.go":    "package lib\n\nfunc Two() int { return 2 }\n",
		".upstream/current/packages/fx/test/widget.test.ts":   "// test\n",
		".upstream/current/packages/fx/test/widgetry.test.ts": "// not widget\n",
	}
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// A row yields one tagged red skeleton next to the cited file; the marker stays `pi-unproven` so portmapcheck does not count it.
func TestSkeletonIsTaggedRedAndUnmarked(t *testing.T) {
	root := fixture(t)
	plan, skipped, err := build(root, []string{"packages/fx/src/group.ts", "packages/fx/src/widget.ts", "packages/fx/src/nothing.ts", "packages/fx/src/skipped.ts"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || len(skipped) != 2 {
		t.Fatalf("plan %d skipped %v, want 2 skeletons and the unciteable and non-✅ rows skipped", len(plan), skipped)
	}
	widget := plan[1]
	if widget.Path != "lib/pi_widget_skeleton_test.go" || widget.Cited != "lib/widget.go" {
		t.Fatalf("widget skeleton %+v", widget)
	}
	if plan[0].Cited != "lib/one.go" || strings.Join(plan[0].Entries, ",") != "One,Two" {
		t.Fatalf("group skeleton %+v: a brace group expands and a missing file is dropped", plan[0])
	}
	if ok, err := write(root, widget); err != nil || !ok {
		t.Fatalf("write: %v %v", ok, err)
	}
	body, _ := os.ReadFile(filepath.Join(root, widget.Path))
	got := string(body)
	for _, want := range []string{"//go:build portmap_skeleton", "package lib", "// pi-unproven: packages/fx/src/widget.ts", "//   - Box.Open", "//   - Widget", "//   - type Box", "//   - packages/fx/test/widget.test.ts", "t.Fatal("} {
		if !strings.Contains(got, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"hidden", "widgetry", "// pi: "} {
		if strings.Contains(got, bad) {
			t.Errorf("skeleton holds %q:\n%s", bad, got)
		}
	}
	if piMarkerLike(got) {
		t.Fatal("a skeleton must not carry a `// pi:` marker")
	}
	if ok, _ := write(root, widget); ok {
		t.Fatal("an existing file was overwritten")
	}
}

func piMarkerLike(src string) bool {
	for l := range strings.SplitSeq(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "// pi:") {
			return true
		}
	}
	return false
}

func TestReadRowsKeepsOnlyMarkerlessRows(t *testing.T) {
	in := filepath.Join(t.TempDir(), "report")
	body := "portmap-check: 3 rows\n" +
		"packages/a/src/x.ts | no exported member in the ledger and no Go test carries `// pi: packages/a/src/x.ts`\n" +
		"packages/a/src/y.ts | COVERAGE: the evidence tests execute nothing\n" +
		"FAIL docs/parity/PORT_MAP.md:1 packages/a/src/z.ts is ✅\n"
	if err := os.WriteFile(in, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pis, err := readRows(in)
	if err != nil || len(pis) != 1 || pis[0] != "packages/a/src/x.ts" {
		t.Fatalf("rows %v err %v", pis, err)
	}
}

// A cell that names a file without code first (embed.go before internal/codingagent/changelog.go) targets the first cited file with a
// function body, because the marker test is proven by mutating a covered statement of its target.
func TestSkeletonTargetsTheFirstCitedFileWithCode(t *testing.T) {
	root := fixture(t)
	files := map[string]string{
		"docs/parity/PORT_MAP.md": "| `packages/fx/src/log.ts` | `embed.go + app/log.go` | ✅ |\n" +
			"| `packages/fx/src/kinds.ts` | `app/kinds.go` | ✅ |\n",
		"embed.go":     "package root\n\nimport _ \"embed\"\n\n//go:embed CHANGELOG.md\nvar Changelog string\n",
		"app/log.go":   "package app\n\nfunc Parse(s string) int { return len(s) }\n",
		"app/kinds.go": "package app\n\ntype Kind int\n\nfunc (Kind) Marker() {}\n",
	}
	for name, body := range files {
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plan, skipped, err := build(root, []string{"packages/fx/src/kinds.ts", "packages/fx/src/log.ts"})
	if err != nil || len(plan) != 2 || len(skipped) != 0 {
		t.Fatalf("plan %+v skipped %v err %v", plan, skipped, err)
	}
	if log := plan[1]; log.Cited != "app/log.go" || log.Path != "app/pi_log_skeleton_test.go" || strings.Join(log.Entries, ",") != "Parse" {
		t.Fatalf("log skeleton %+v, want it next to app/log.go", log)
	}
	if kinds := plan[0]; kinds.Cited != "app/kinds.go" {
		t.Fatalf("kinds skeleton %+v: a cell with no code falls back to its first cited file", kinds)
	}
}

// -skip drops the Pi files another branch already marks; comments and extra fields are ignored.
func TestSkipListExcludesRowsMarkedElsewhere(t *testing.T) {
	list := filepath.Join(t.TempDir(), "marked")
	if err := os.WriteFile(list, []byte("# marked on lg-imla-8\npackages/a/src/x.ts\tagent/x_test.go\n\npackages/a/src/z.ts # in flight\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	excluded, err := readSkip(list)
	if err != nil {
		t.Fatal(err)
	}
	kept, skipped := exclude([]string{"packages/a/src/x.ts", "packages/a/src/y.ts", "packages/a/src/z.ts"}, excluded)
	if strings.Join(kept, ",") != "packages/a/src/y.ts" || len(skipped) != 2 || !strings.HasPrefix(skipped[0], "packages/a/src/x.ts | ") {
		t.Fatalf("kept %v skipped %v", kept, skipped)
	}
}

// A Pi test belongs to the longest source stem it names: agent-session-runtime.test.ts tests agent-session-runtime.ts, not agent-session.ts,
// while agent-session-retry.test.ts (no agent-session-retry.ts) stays with agent-session.ts.
func TestPiTestsBelongToTheLongestSourceStem(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{
		"packages/ca/src/core/agent-session.ts", "packages/ca/src/core/agent-session-runtime.ts", "packages/ca/src/core/types.d.ts",
		"packages/ca/test/agent-session-retry.test.ts", "packages/ca/test/agent-session-runtime.test.ts",
		"packages/ca/test/suite/agent-session-runtime-events.test.ts", "packages/ca/test/agent-session.test.ts",
	} {
		full := filepath.Join(root, ".upstream/current", name)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("//\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	idx := piTestIndex(root)
	if got := strings.Join(idx.forSource("packages/ca/src/core/agent-session.ts"), ","); got != "packages/ca/test/agent-session-retry.test.ts,packages/ca/test/agent-session.test.ts" {
		t.Fatalf("agent-session.ts tests %s", got)
	}
	if got := strings.Join(idx.forSource("packages/ca/src/core/agent-session-runtime.ts"), ","); got != "packages/ca/test/agent-session-runtime.test.ts,packages/ca/test/suite/agent-session-runtime-events.test.ts" {
		t.Fatalf("agent-session-runtime.ts tests %s", got)
	}
}
