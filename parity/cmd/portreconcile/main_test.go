package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
)

// buildFixture writes a tiny repo whose inventory, PORT_MAP, and Go tree exercise
// every classification branch: an audited port, an implemented-but-unaudited
// declaration, a designed-out note, a partial, a member that must roll up, and a
// genuine gap whose mapped Go target is absent.
func buildFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, content string) {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Existing Go targets.
	write("agent/agent.go", "package agent\n")
	write("ai/oauth_registry.go", "package ai\n")
	write("internal/codingagent/session_manager.go", "package codingagent\n")

	write("PORT_MAP.md", "| upstream | target | status |\n|---|---|---|\n"+
		"| `packages/agent/src/agent-loop.ts` | `agent/agent.go` | ✅ |\n"+
		"| `packages/agent/src/agent.ts` | `agent/agent.go` | ⬜ |\n"+
		"| `packages/ai/src/auth/types.ts` | `ai/auth.go + ai/oauth_*.go` | ⬜ |\n"+
		"| `packages/agent/src/proxy.ts` | `(not needed: no TS proxy)` | ⬜ |\n"+
		"| `packages/coding-agent/src/core/session-manager.ts` | `internal/codingagent/session_manager.go` | 🟡 |\n"+
		"| `packages/tui/src/gone.ts` | `tui/gone.go` | ⬜ |\n")

	inv := `{"interfaces":[
      {"id":"pkg:agent/.#AgentLoop","package":"@earendil-works/pi-agent-core","name":"AgentLoop","source":{"path":"node_modules/@earendil-works/pi-agent-core/dist/agent-loop.d.ts"}},
      {"id":"pkg:agent/.#Agent","package":"@earendil-works/pi-agent-core","name":"Agent","source":{"path":"node_modules/@earendil-works/pi-agent-core/dist/agent.d.ts"}},
      {"id":"pkg:ai/.#AuthType","package":"@earendil-works/pi-ai","name":"AuthType","source":{"path":"node_modules/@earendil-works/pi-ai/dist/auth/types.d.ts"}},
      {"id":"pkg:ai/.#AuthType::property:kind","parentId":"pkg:ai/.#AuthType","package":"@earendil-works/pi-ai","name":"AuthType.kind","source":{"path":"node_modules/@earendil-works/pi-ai/dist/auth/types.d.ts"}},
      {"id":"pkg:agent/.#Proxy","package":"@earendil-works/pi-agent-core","name":"Proxy","source":{"path":"node_modules/@earendil-works/pi-agent-core/dist/proxy.d.ts"}},
      {"id":"pkg:coding-agent/.#SessionManager","package":"@earendil-works/pi-coding-agent","name":"SessionManager","source":{"path":"dist/core/session-manager.d.ts"}},
      {"id":"pkg:tui/.#Gone","package":"@earendil-works/pi-tui","name":"Gone","source":{"path":"node_modules/@earendil-works/pi-tui/dist/gone.d.ts"}},
      {"id":"pkg:tui/.#Orphan","package":"@earendil-works/pi-tui","name":"Orphan","source":{"path":"node_modules/@earendil-works/pi-tui/dist/orphan.d.ts"}}
    ]}`
	write("parity/interfaces/upstream-v"+coding.UpstreamVersion+".json", inv)
	return root
}

func TestUpstreamSourcePathUsesTrackedDependencyProvenanceForReexport(t *testing.T) {
	decl := declaration{Package: "@earendil-works/pi-coding-agent"}
	decl.Source.Path = "node_modules/@earendil-works/pi-agent-core/dist/types.d.ts"

	got, ok := upstreamSourcePath(decl)
	if !ok {
		t.Fatal("upstreamSourcePath rejected tracked dependency provenance")
	}
	if want := "packages/agent/src/types.ts"; got != want {
		t.Fatalf("upstreamSourcePath = %q, want %q", got, want)
	}
}

func TestReconcileClassifiesEveryBranch(t *testing.T) {
	root := buildFixture(t)
	decls, err := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	if err != nil {
		t.Fatal(err)
	}
	report := reconcile(root, decls, rows, nil)

	if report.total != 7 {
		t.Fatalf("total top-level = %d, want 7 (members must roll up)", report.total)
	}
	want := map[classification]int{
		classPortedAudited:        1, // AgentLoop ✅
		classImplementedUnaudited: 2, // Agent ⬜ + AuthType ⬜ (glob resolves)
		classDesignedOut:          1, // Proxy note
		classPartialDeferred:      1, // SessionManager 🟡
		classGap:                  1, // Gone ⬜ target absent
		classUnjoined:             1, // Orphan: no PORT_MAP row
	}
	for class, count := range want {
		if report.counts[class] != count {
			t.Errorf("%s = %d, want %d", class, report.counts[class], count)
		}
	}
	if len(report.gaps) != 1 || report.gaps[0] != "pkg:tui/.#Gone" {
		t.Fatalf("gaps = %v, want [pkg:tui/.#Gone]", report.gaps)
	}
}

// TestUnauditedRequiresGoFile proves the honest distinction: an unaudited row is
// only "implemented" when its mapped Go file exists. Delete the file and it must
// flip to a gap: the classifier cannot mint "implemented" from a ⬜ glyph alone.
func TestUnauditedRequiresGoFile(t *testing.T) {
	root := buildFixture(t)
	if err := os.Remove(filepath.Join(root, "agent", "agent.go")); err != nil {
		t.Fatal(err)
	}
	decls, _ := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	rows, _ := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	report := reconcile(root, decls, rows, nil)
	// Agent (⬜) now has no Go target; AgentLoop (✅) stays audited regardless.
	if report.counts[classGap] != 2 {
		t.Fatalf("gap-candidates = %d, want 2 after removing agent.go", report.counts[classGap])
	}
	if report.counts[classPortedAudited] != 1 {
		t.Fatalf("ported-audited = %d, want 1 (✅ is disposition, not file check)", report.counts[classPortedAudited])
	}
}

func TestDeferredDispositionIsNotDesignedOutByNoteTarget(t *testing.T) {
	decl := declaration{Package: "@earendil-works/pi-agent-core"}
	decl.Source.Path = "node_modules/@earendil-works/pi-agent-core/dist/proxy.d.ts"
	rows := map[string]portMapRow{
		"packages/agent/src/proxy.ts": {
			target:      "(deferred: no Go target yet)",
			disposition: "⏸",
		},
	}

	if got := classify(t.TempDir(), decl, rows, nil); got != classPartialDeferred {
		t.Fatalf("classify deferred note = %s, want %s", got, classPartialDeferred)
	}
}

// TestDesignedOutNoteNeverGap proves a note target (no Go file) is designed-out,
// never a gap, so intentional omissions do not inflate the missing count.
func TestDesignedOutNoteNeverGap(t *testing.T) {
	root := buildFixture(t)
	decls, _ := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	rows, _ := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	report := reconcile(root, decls, rows, nil)
	if report.counts[classDesignedOut] != 1 {
		t.Fatalf("designed-out = %d, want 1", report.counts[classDesignedOut])
	}
}

// TestCoverageSplitsImplemented proves that supplying a coverage profile splits
// implemented-unaudited into covered vs uncovered: a mapped file executed by a
// passing flow is implemented-covered, an equally-mapped but unexecuted file is
// implemented-uncovered: the honest floor that flags file-exists-but-dead code.
func TestCoverageSplitsImplemented(t *testing.T) {
	root := buildFixture(t)
	// agent.go (Agent ⬜) is covered; ai/oauth_registry.go (AuthType ⬜) is not.
	piglet := "mode: set\n" +
		modulePrefix + "agent/agent.go:1.1,2.1 1 1\n" +
		modulePrefix + "ai/oauth_registry.go:1.1,2.1 1 0\n"
	profPath := filepath.Join(root, "cover.out")
	if err := os.WriteFile(profPath, []byte(piglet), 0o644); err != nil {
		t.Fatal(err)
	}
	covered, err := coveredFiles(profPath)
	if err != nil {
		t.Fatal(err)
	}
	decls, _ := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	rows, _ := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	report := reconcile(root, decls, rows, covered)

	if report.counts[classImplementedCovered] != 1 {
		t.Errorf("implemented-covered = %d, want 1 (Agent -> agent.go executed)", report.counts[classImplementedCovered])
	}
	if report.counts[classImplementedUncovered] != 1 {
		t.Errorf("implemented-uncovered = %d, want 1 (AuthType -> oauth_registry.go count 0)", report.counts[classImplementedUncovered])
	}
	if report.counts[classImplementedUnaudited] != 0 {
		t.Errorf("implemented-unaudited = %d, want 0 when coverage supplied", report.counts[classImplementedUnaudited])
	}
}

// TestReconcileGroupsBundlesByUpstreamDir proves the review chunking: top-level
// declarations aggregate by upstream source directory (members already excluded),
// unjoinable declarations share one bucket, and each group carries the parity
// scenarios whose covers list names a file in it.
func TestReconcileGroupsBundlesByUpstreamDir(t *testing.T) {
	root := buildFixture(t)
	scenariosRoot := filepath.Join(root, "parity", "scenarios", "fam")
	if err := os.MkdirAll(scenariosRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scenariosRoot, "01-auth.toml"),
		[]byte("covers = [\"packages/ai/src/auth/types.ts\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	index, err := scenarioCoverage(filepath.Join(root, "parity", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	if got := index["packages/ai/src/auth"]; len(got) != 1 || got[0] != "fam/01-auth" {
		t.Fatalf("scenario index for auth = %v, want [fam/01-auth]", got)
	}

	decls, _ := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	rows, _ := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	groups := reconcileGroups(root, decls, rows, nil, nil, nil, index)

	byKey := map[string]group{}
	for _, g := range groups {
		byKey[g.key] = g
	}
	if g := byKey["packages/agent/src"]; g.total != 3 {
		t.Errorf("agent/src group total = %d, want 3 (AgentLoop+Agent+Proxy)", g.total)
	}
	if g := byKey["packages/ai/src/auth"]; len(g.scenarios) != 1 || g.scenarios[0] != "fam/01-auth" {
		t.Errorf("auth group scenarios = %v, want [fam/01-auth]", g.scenarios)
	}
	// Gone (gap) and Orphan (no PORT_MAP row) both live in packages/tui/src, so a
	// derivable source dir groups them together even when unmapped.
	if g := byKey["packages/tui/src"]; g.total != 2 {
		t.Errorf("tui/src group total = %d, want 2 (Gone+Orphan)", g.total)
	}
	if _, ok := byKey["(unplaceable)"]; ok {
		t.Errorf("no declaration should be unplaceable in this fixture")
	}
	total := 0
	for _, g := range groups {
		total += g.total
	}
	if total != 7 {
		t.Fatalf("group totals sum to %d, want 7 (every top-level lands in exactly one group)", total)
	}
}

// TestCoverageTiersRankImplementedEvidence proves the confidence tiering a
// reviewer reads: an implemented declaration whose mapped file is exercised by a
// byte-faithful parity flow ranks "parity" (strong); one exercised only by a unit
// test ranks "unit" (floor); audited/designed-out/gap declarations are not tiered.
func TestCoverageTiersRankImplementedEvidence(t *testing.T) {
	root := buildFixture(t)
	// agent.go is in BOTH sets, so parity must win over unit (precedence).
	parityCov := map[string]bool{"agent/agent.go": true}
	unitCov := map[string]bool{"agent/agent.go": true, "ai/oauth_registry.go": true} // AuthType -> unit only
	decls, _ := loadInventory(filepath.Join(root, "parity", "interfaces", "upstream-v"+coding.UpstreamVersion+".json"))
	rows, _ := parsePortMap(filepath.Join(root, "PORT_MAP.md"))
	groups := reconcileGroups(root, decls, rows, nil, parityCov, unitCov, nil)

	byKey := map[string]group{}
	for _, g := range groups {
		byKey[g.key] = g
	}
	if g := byKey["packages/agent/src"]; g.tiers["parity"] != 1 || g.tiers["unit"] != 0 {
		t.Errorf("agent/src tiers = %v, want parity 1 (Agent), AgentLoop ✅ untiered", g.tiers)
	}
	if g := byKey["packages/ai/src/auth"]; g.tiers["unit"] != 1 || g.tiers["parity"] != 0 {
		t.Errorf("ai/auth tiers = %v, want unit 1 (AuthType floor)", g.tiers)
	}
	// Gone is a gap (mapped file absent) and must not be tiered.
	if g := byKey["packages/tui/src"]; g.tiers["none"] != 0 {
		t.Errorf("tui/src tiers = %v, want gap/unmapped untiered", g.tiers)
	}
}
