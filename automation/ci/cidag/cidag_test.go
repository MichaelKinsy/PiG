// SPDX-License-Identifier: MIT

package cidag

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func buildFixtureGraph(t *testing.T) (*Graph, map[string]string) {
	t.Helper()
	root := "/repo"
	mod := DefaultModule
	sources := map[string]string{
		"/repo/ai/openai.go":                                 "package ai\nfunc OpenAI() {}\n",
		"/repo/ai/openai_test.go":                            "package ai\n",
		"/repo/ai/auth_windows_test.go":                      "package ai\n",
		"/repo/agent/loop.go":                                "package agent\nimport _ \"github.com/MichaelKinsy/PiG/ai\"\n",
		"/repo/agent/loop_test.go":                           "package agent\n",
		"/repo/coding/session.go":                            "package coding\nimport _ \"github.com/MichaelKinsy/PiG/agent\"\n",
		"/repo/coding/session_windows_test.go":               "package coding\n",
		"/repo/coding/cli/cli.go":                            "package cli\nimport _ \"github.com/MichaelKinsy/PiG/coding\"\n",
		"/repo/coding/cli/cli_test.go":                       "package cli\n",
		"/repo/cmd/pig/main.go":                              "package main\nimport _ \"github.com/MichaelKinsy/PiG/coding/cli\"\n",
		"/repo/tui/editor.go":                                "package tui\nimport \"runtime\"\nvar _ = runtime.GOOS\n",
		"/repo/tui/editor_darwin_test.go":                    "package tui\n",
		"/repo/tui/editor_windows.go":                        "//go:build windows\npackage tui\n",
		"/repo/internal/embedder/embedder.go":                "package embedder\nimport _ \"embed\"\n//go:embed data.txt\nvar D string\n",
		"/repo/internal/embedder/embedder_test.go":           "package embedder\nimport _ \"embed\"\n//go:embed fixture.json\nvar F string\n",
		"/repo/internal/consumer/consumer.go":                "package consumer\nimport _ \"github.com/MichaelKinsy/PiG/internal/embedder\"\n",
		"/repo/internal/refpkg/ref.go":                       "package refpkg\nvar P = \"internal/assets/config.json\"\n",
		"/repo/internal/refpkg/ref_test.go":                  "package refpkg\nvar T = filepath.Join(\"internal\", \"assets\", \"test_only.json\")\n",
		"/repo/internal/refconsumer/rc.go":                   "package refconsumer\nimport _ \"github.com/MichaelKinsy/PiG/internal/refpkg\"\n",
		"/repo/internal/winonly/win.go":                      "//go:build windows\npackage winonly\nimport _ \"github.com/MichaelKinsy/PiG/ai\"\n",
		"/repo/internal/winonly/win_test.go":                 "//go:build windows\npackage winonly\n",
		"/repo/internal/testenv/env.go":                      "package testenv\n",
		"/repo/test/ci-images/ci_test.go":                    "package ciimages\n",
		"/repo/test/docs-drift/drift_test.go":                "package docsdrift\n",
		"/repo/coding/extension/host/subprocess/sub.go":      "package subprocess\n",
		"/repo/coding/extension/host/subprocess/protocol.go": "package subprocess\n",
		"/repo/test/extension-conformance/conf_test.go":      "package conformance\n",
		"/repo/internal/orphan/exotic.go":                    "//go:build plan9\npackage orphan\n",
	}

	readFn := func(abs string) ([]byte, error) {
		if body, ok := sources[filepath.ToSlash(abs)]; ok {
			return []byte(body), nil
		}
		return nil, os.ErrNotExist
	}

	modInfo := &ModInfo{Path: mod}
	common := []ListPackage{
		{
			ImportPath: mod + "/ai", Dir: "/repo/ai", Module: modInfo,
			GoFiles: []string{"openai.go"}, TestGoFiles: []string{"openai_test.go"},
		},
		{
			ImportPath: mod + "/agent", Dir: "/repo/agent", Module: modInfo,
			GoFiles: []string{"loop.go"}, TestGoFiles: []string{"loop_test.go"},
			Imports: []string{mod + "/ai"}, Deps: []string{mod + "/ai"},
		},
		{
			ImportPath: mod + "/coding", Dir: "/repo/coding", Module: modInfo,
			GoFiles: []string{"session.go"},
			Imports: []string{mod + "/agent"}, Deps: []string{mod + "/agent", mod + "/ai"},
		},
		{
			ImportPath: mod + "/coding/cli", Dir: "/repo/coding/cli", Module: modInfo,
			GoFiles: []string{"cli.go"}, TestGoFiles: []string{"cli_test.go"},
			Imports: []string{mod + "/coding"}, Deps: []string{mod + "/coding", mod + "/agent", mod + "/ai"},
		},
		{
			ImportPath: mod + "/cmd/pig", Dir: "/repo/cmd/pig", Module: modInfo,
			GoFiles: []string{"main.go"},
			Imports: []string{mod + "/coding/cli"}, Deps: []string{mod + "/coding/cli", mod + "/coding", mod + "/agent", mod + "/ai"},
		},
		{
			ImportPath: mod + "/tui", Dir: "/repo/tui", Module: modInfo,
			GoFiles: []string{"editor.go"}, IgnoredGoFiles: []string{"editor_windows.go"},
		},
		{
			ImportPath: mod + "/internal/embedder", Dir: "/repo/internal/embedder", Module: modInfo,
			GoFiles: []string{"embedder.go"}, TestGoFiles: []string{"embedder_test.go"},
			EmbedFiles: []string{"data.txt"}, TestEmbedFiles: []string{"fixture.json"},
		},
		{
			ImportPath: mod + "/internal/consumer", Dir: "/repo/internal/consumer", Module: modInfo,
			GoFiles: []string{"consumer.go"},
			Imports: []string{mod + "/internal/embedder"}, Deps: []string{mod + "/internal/embedder"},
		},
		{
			ImportPath: mod + "/internal/refpkg", Dir: "/repo/internal/refpkg", Module: modInfo,
			GoFiles: []string{"ref.go"}, TestGoFiles: []string{"ref_test.go"},
		},
		{
			ImportPath: mod + "/internal/refconsumer", Dir: "/repo/internal/refconsumer", Module: modInfo,
			GoFiles: []string{"rc.go"},
			Imports: []string{mod + "/internal/refpkg"}, Deps: []string{mod + "/internal/refpkg"},
		},
		{
			ImportPath: mod + "/internal/testenv", Dir: "/repo/internal/testenv", Module: modInfo,
			GoFiles: []string{"env.go"},
		},
		{
			ImportPath: mod + "/test/ci-images", Dir: "/repo/test/ci-images", Module: modInfo,
			TestGoFiles: []string{"ci_test.go"},
		},
		{
			ImportPath: mod + "/test/docs-drift", Dir: "/repo/test/docs-drift", Module: modInfo,
			TestGoFiles: []string{"drift_test.go"},
		},
		{
			ImportPath: mod + "/coding/extension/host/subprocess", Dir: "/repo/coding/extension/host/subprocess", Module: modInfo,
			GoFiles: []string{"sub.go", "protocol.go"},
		},
		{
			ImportPath: mod + "/test/extension-conformance", Dir: "/repo/test/extension-conformance", Module: modInfo,
			TestGoFiles: []string{"conf_test.go"},
		},
		{
			ImportPath: mod + "/internal/orphan", Dir: "/repo/internal/orphan", Module: modInfo,
			IgnoredGoFiles: []string{"exotic.go"},
		},
	}

	linuxPkgs := append([]ListPackage(nil), common...)
	linuxPkgs = append(linuxPkgs, ListPackage{
		ImportPath: mod + "/internal/winonly", Dir: "/repo/internal/winonly", Module: modInfo,
		IgnoredGoFiles: []string{"win.go", "win_test.go"},
	})

	winPkgs := append([]ListPackage(nil), common...)
	winPkgs = append(winPkgs,
		ListPackage{
			ImportPath: mod + "/ai", Dir: "/repo/ai", Module: modInfo,
			GoFiles: []string{"openai.go"}, TestGoFiles: []string{"openai_test.go", "auth_windows_test.go"},
		},
		ListPackage{
			ImportPath: mod + "/coding", Dir: "/repo/coding", Module: modInfo,
			GoFiles: []string{"session.go"}, TestGoFiles: []string{"session_windows_test.go"},
			Imports: []string{mod + "/agent"}, Deps: []string{mod + "/agent", mod + "/ai"},
		},
		ListPackage{
			ImportPath: mod + "/tui", Dir: "/repo/tui", Module: modInfo,
			GoFiles: []string{"editor.go", "editor_windows.go"},
		},
		ListPackage{
			ImportPath: mod + "/internal/winonly", Dir: "/repo/internal/winonly", Module: modInfo,
			GoFiles: []string{"win.go"}, TestGoFiles: []string{"win_test.go"},
			Imports: []string{mod + "/ai"}, Deps: []string{mod + "/ai"},
		},
	)

	darwinPkgs := append([]ListPackage(nil), common...)
	darwinPkgs = append(darwinPkgs,
		ListPackage{
			ImportPath: mod + "/tui", Dir: "/repo/tui", Module: modInfo,
			GoFiles: []string{"editor.go"}, TestGoFiles: []string{"editor_darwin_test.go"},
			IgnoredGoFiles: []string{"editor_windows.go"},
		},
		ListPackage{
			ImportPath: mod + "/internal/winonly", Dir: "/repo/internal/winonly", Module: modInfo,
			IgnoredGoFiles: []string{"win.go", "win_test.go"},
		},
	)

	g, err := BuildGraph(GraphInput{
		Root:   root,
		Module: mod,
		PackagesByGOOS: map[string][]ListPackage{
			"linux":   linuxPkgs,
			"windows": winPkgs,
			"darwin":  darwinPkgs,
		},
		PortMapEdges: map[string][]string{
			"packages/ai/src/openai.ts":        {"ai/openai.go"},
			"packages/coding-agent/src/cli.ts": {"coding/cli/cli.go"},
		},
		ScenarioCovers: map[string][]string{
			"test/parity/scenarios/providers/01-openai.toml": {"packages/ai/src/openai.ts"},
			"test/parity/scenarios/cli/01-cli.toml":          {"packages/coding-agent/src/cli.ts"},
			"test/parity/scenarios/unmapped/01-noedge.toml":  {"packages/unknown/src/x.ts"},
		},
		ScenarioTexts: map[string]string{
			"test/parity/scenarios/providers/01-openai.toml": `name = "01-openai" fixture = "test/parity/testdata/openai-probe.mjs"`,
			"test/parity/scenarios/cli/01-cli.toml":          `name = "01-cli"`,
			"test/parity/scenarios/unmapped/01-noedge.toml":  `name = "01-noedge"`,
		},
		ReadFile: readFn,
	})
	if err != nil {
		t.Fatalf("BuildGraph: %v", err)
	}
	return g, sources
}

func readFromMap(sources map[string]string) func(string) ([]byte, error) {
	return func(rel string) ([]byte, error) {
		if body, ok := sources["/repo/"+rel]; ok {
			return []byte(body), nil
		}
		return nil, os.ErrNotExist
	}
}

func TestCompileVsTestOnlyEdges(t *testing.T) {
	g, sources := buildFixtureGraph(t)

	// Compile edge: editing ai/openai.go triggers transitive reverse closure:
	// ai -> agent -> coding -> coding/cli -> cmd/pig AND cross-GOOS internal/winonly.
	compilePlan := Select([]string{"ai/openai.go"}, g, DefaultRules, Options{ReadFile: readFromMap(sources)})
	if compilePlan.Full {
		t.Fatalf("expected selective plan for ai/openai.go, got full: %v", compilePlan.FullReason)
	}
	for _, wantPkg := range []string{"./ai", "./agent", "./coding", "./coding/cli", "./cmd/pig", "./internal/winonly"} {
		if !slices.Contains(compilePlan.AffectedPackages, wantPkg) {
			t.Errorf("compile edge missing %s in affected packages %v", wantPkg, compilePlan.AffectedPackages)
		}
	}
	// Cross-GOOS union must include Windows-only test lanes for ai, coding, winonly.
	for _, wantLane := range []string{"preflight", "build", "checks", "contracts", "test-fast-sharded", "test-fast-sharded-2", "test-cli-1", "win-native-1", "win-native-2", "win-native-4", "win-cli", "parity-1"} {
		if !slices.Contains(compilePlan.Lanes, wantLane) {
			t.Errorf("compile edge missing lane %s in %v", wantLane, compilePlan.Lanes)
		}
	}
	// Global guard packages are always present for non-docs changes.
	for _, guard := range GlobalGuardPackages {
		if !slices.Contains(compilePlan.AffectedPackages, guard) {
			t.Errorf("missing global guard package %s in %v", guard, compilePlan.AffectedPackages)
		}
	}

	// Test-only edge: editing ai/openai_test.go selects ONLY ./ai (+ global guards), NOT agent/coding/cli.
	testOnlyPlan := Select([]string{"ai/openai_test.go"}, g, DefaultRules, Options{ReadFile: readFromMap(sources)})
	if testOnlyPlan.Full {
		t.Fatalf("expected selective plan for ai/openai_test.go, got full: %v", testOnlyPlan.FullReason)
	}
	if !slices.Contains(testOnlyPlan.TestOnlyPackages, "./ai") || len(testOnlyPlan.CompilePackages) != 0 {
		t.Errorf("expected test-only=[./ai] compile=[], got test-only=%v compile=%v", testOnlyPlan.TestOnlyPackages, testOnlyPlan.CompilePackages)
	}
	for _, unexpected := range []string{"./agent", "./coding", "./coding/cli", "./cmd/pig"} {
		if slices.Contains(testOnlyPlan.AffectedPackages, unexpected) {
			t.Errorf("test-only edge leaked into reverse dependent %s: affected=%v", unexpected, testOnlyPlan.AffectedPackages)
		}
	}
	if slices.Contains(testOnlyPlan.Lanes, "win-cli") || slices.Contains(testOnlyPlan.Lanes, "test-cli-1") {
		t.Errorf("test-only edge should not select cli lanes: %v", testOnlyPlan.Lanes)
	}
	if len(testOnlyPlan.ScenariosClosure) != 0 {
		t.Errorf("test-only edge should not trigger parity scenarios: %v", testOnlyPlan.ScenariosClosure)
	}
}

func TestEmbedAndTestdataAndReferencingEdges(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	// Production //go:embed file is a compile edge -> reverse closure includes internal/consumer.
	embedSrcPlan := Select([]string{"internal/embedder/data.txt"}, g, DefaultRules, opts)
	if embedSrcPlan.Full {
		t.Fatalf("embed source file failed closed to full: %v", embedSrcPlan.FullReason)
	}
	if !slices.Contains(embedSrcPlan.CompilePackages, "./internal/embedder") {
		t.Errorf("embed source should be compile edge for ./internal/embedder, got %v", embedSrcPlan.CompilePackages)
	}
	if !slices.Contains(embedSrcPlan.AffectedPackages, "./internal/consumer") {
		t.Errorf("embed source reverse closure should include ./internal/consumer, got %v", embedSrcPlan.AffectedPackages)
	}

	// TestEmbedFiles is a test-only edge -> only ./internal/embedder, NOT ./internal/consumer.
	embedTestPlan := Select([]string{"internal/embedder/fixture.json"}, g, DefaultRules, opts)
	if embedTestPlan.Full {
		t.Fatalf("test embed file failed closed to full: %v", embedTestPlan.FullReason)
	}
	if !slices.Contains(embedTestPlan.TestOnlyPackages, "./internal/embedder") {
		t.Errorf("test embed should be test-only edge for ./internal/embedder, got %v", embedTestPlan.TestOnlyPackages)
	}
	if slices.Contains(embedTestPlan.AffectedPackages, "./internal/consumer") {
		t.Errorf("test embed should not pull ./internal/consumer: %v", embedTestPlan.AffectedPackages)
	}

	// testdata/ file is a test-only edge to enclosing package.
	testdataPlan := Select([]string{"internal/embedder/testdata/case1.json"}, g, DefaultRules, opts)
	if testdataPlan.Full {
		t.Fatalf("testdata file failed closed to full: %v", testdataPlan.FullReason)
	}
	if !slices.Contains(testdataPlan.TestOnlyPackages, "./internal/embedder") || slices.Contains(testdataPlan.AffectedPackages, "./internal/consumer") {
		t.Errorf("testdata should select only ./internal/embedder, got compile=%v testonly=%v affected=%v",
			testdataPlan.CompilePackages, testdataPlan.TestOnlyPackages, testdataPlan.AffectedPackages)
	}

	// String-referenced non-Go file in production source -> compile edge -> includes refconsumer.
	refProdPlan := Select([]string{"internal/assets/config.json"}, g, DefaultRules, opts)
	if refProdPlan.Full {
		t.Fatalf("referenced prod asset failed closed to full: %v", refProdPlan.FullReason)
	}
	if !slices.Contains(refProdPlan.AffectedPackages, "./internal/refconsumer") {
		t.Errorf("prod string-referenced asset should reach ./internal/refconsumer, got %v", refProdPlan.AffectedPackages)
	}

	// filepath.Join-referenced non-Go file in _test.go -> test-only edge -> only refpkg.
	refTestPlan := Select([]string{"internal/assets/test_only.json"}, g, DefaultRules, opts)
	if refTestPlan.Full {
		t.Fatalf("referenced test asset failed closed to full: %v", refTestPlan.FullReason)
	}
	if !slices.Contains(refTestPlan.TestOnlyPackages, "./internal/refpkg") || slices.Contains(refTestPlan.AffectedPackages, "./internal/refconsumer") {
		t.Errorf("test string-referenced asset should select only ./internal/refpkg, got affected=%v", refTestPlan.AffectedPackages)
	}
}

func TestFailClosedConditions(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	// 1. Unknown/unowned file -> full=true.
	pUnowned := Select([]string{"mystery/unowned.bin"}, g, DefaultRules, opts)
	if !pUnowned.Full || !slices.Contains(pUnowned.Unowned, "mystery/unowned.bin") {
		t.Errorf("unowned file must fail closed to full: full=%v unowned=%v", pUnowned.Full, pUnowned.Unowned)
	}
	if !slices.Equal(pUnowned.Lanes, AllLanes()) {
		t.Errorf("full plan must contain every lane")
	}

	// 2. Go file ignored across all queried GOOS (not //go:build ignore) -> unowned -> full=true (S4).
	pOrphan := Select([]string{"internal/orphan/exotic.go"}, g, DefaultRules, opts)
	if !pOrphan.Full || !slices.Contains(pOrphan.Unowned, "internal/orphan/exotic.go") {
		t.Errorf("all-GOOS ignored Go file must fail closed to full: full=%v unowned=%v", pOrphan.Full, pOrphan.Unowned)
	}

	// 3. More than MaxFiles changed -> full=true.
	many := make([]string, 301)
	for i := range many {
		many[i] = "docs/file_" + string(rune('a'+i%26)) + "_" + string(rune('a'+(i/26)%26)) + ".md"
	}
	pMany := Select(many, g, DefaultRules, opts)
	if !pMany.Full {
		t.Errorf(">300 files must fail closed to full")
	}

	// 4. Nil graph (tool error) -> full=true.
	pNilGraph := Select([]string{"ai/openai.go"}, nil, DefaultRules, opts)
	if !pNilGraph.Full {
		t.Errorf("nil graph must fail closed to full")
	}

	// 5. CI definition file (RuleFull) -> full=true.
	pCI := Select([]string{".github/workflows/ci.yml"}, g, DefaultRules, opts)
	if !pCI.Full {
		t.Errorf("workflow edit must trigger full plan")
	}
}

func TestCrossGOOSUnionAndOSSpecificPromotion(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	// Windows-only Go file is still owned via GOOS=windows graph union and triggers OS-specific promotion.
	pWin := Select([]string{"tui/editor_windows.go"}, g, DefaultRules, opts)
	if pWin.Full {
		t.Fatalf("tui/editor_windows.go should be owned via windows graph: %v", pWin.FullReason)
	}
	if !pWin.OSSpecific {
		t.Errorf("expected OSSpecific=true for tui/editor_windows.go")
	}
	if !slices.Contains(pWin.Lanes, "mac-native") || !slices.Contains(pWin.PRLanes, "mac-native") {
		t.Errorf("expected darwin test package tui to be selected and promoted on OS-specific edit: lanes=%v pr=%v", pWin.Lanes, pWin.PRLanes)
	}

	// Cross-GOOS package built only on Windows (internal/winonly) is owned and selects win-native-4.
	pWinOnly := Select([]string{"internal/winonly/win.go"}, g, DefaultRules, opts)
	if pWinOnly.Full {
		t.Fatalf("internal/winonly/win.go should be owned via windows GOOS union: %v", pWinOnly.FullReason)
	}
	if !slices.Contains(pWinOnly.Lanes, "win-native-4") {
		t.Errorf("expected win-native-4 for Windows-only test package, got %v", pWinOnly.Lanes)
	}
}

func TestDocsOnlyVsGlobalGateLockIn(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	docsPlan := Select([]string{"README.md", "docs/media/banner.png"}, g, DefaultRules, opts)
	if !docsPlan.DocsOnly || docsPlan.Full || len(docsPlan.Lanes) != 0 {
		t.Errorf("docs-only change expected DocsOnly=true Full=false Lanes=[], got %+v", docsPlan)
	}

	// Non-docs change (even a changelog fragment) MUST lock in GlobalGateLanes and GlobalGuardPackages.
	fragPlan := Select([]string{"changelog.d/demo.md"}, g, DefaultRules, opts)
	if fragPlan.DocsOnly || fragPlan.Full {
		t.Fatalf("changelog fragment should be non-docs selective plan: %+v", fragPlan)
	}
	for _, gate := range GlobalGateLanes {
		if !slices.Contains(fragPlan.Lanes, gate) || !slices.Contains(fragPlan.PRLanes, gate) {
			t.Errorf("missing global gate %s in lanes=%v pr_lanes=%v", gate, fragPlan.Lanes, fragPlan.PRLanes)
		}
	}
	for _, guard := range GlobalGuardPackages {
		if !slices.Contains(fragPlan.AffectedPackages, guard) {
			t.Errorf("missing global guard %s in affected=%v", guard, fragPlan.AffectedPackages)
		}
	}
}

func TestShadowAuditBlindSpotDetection(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	plan := Select([]string{"ai/openai_test.go"}, g, DefaultRules, opts)

	// Covered failure: test-fast-sharded was selected -> OK=true, zero blind spots.
	okReport := AuditRun(plan, []string{"test-fast-sharded", "win-native-2"}, "full", "run-1", "abc123")
	if !okReport.OK || len(okReport.BlindSpots) != 0 || len(okReport.CoveredLanes) != 2 {
		t.Errorf("expected clean audit report, got %+v", okReport)
	}

	// Blind spot: win-cli failed in full run, but DAG skipped it -> OK=false, BlindSpots=["win-cli"].
	missReport := AuditRun(plan, []string{"test-fast-sharded", "win-cli"}, "full", "run-2", "def456")
	if missReport.OK || !slices.Equal(missReport.BlindSpots, []string{"win-cli"}) {
		t.Errorf("expected blind spot [win-cli], got %+v", missReport)
	}

	// PR-tier audit detects queue-only lane misses when auditing push tier.
	prMiss := AuditRun(plan, []string{"win-native-2"}, "pr", "run-3", "ghi789")
	if prMiss.OK || !slices.Equal(prMiss.BlindSpots, []string{"win-native-2"}) {
		t.Errorf("expected PR-tier blind spot [win-native-2], got %+v", prMiss)
	}
}

func TestParityScenarioSelectionAndFixtureFailClosed(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	opts := Options{ReadFile: readFromMap(sources)}

	// Direct edit to ai/openai.go selects 01-openai (direct/dir/closure) + 01-noedge (unmapped), and
	// 01-cli is selected in closure (because coding/cli depends on ai) but NOT in direct/dir!
	p := Select([]string{"ai/openai.go"}, g, DefaultRules, opts)
	if !slices.Contains(p.ScenariosDirect, "test/parity/scenarios/providers/01-openai.toml") {
		t.Errorf("expected direct scenario 01-openai, got %v", p.ScenariosDirect)
	}
	if slices.Contains(p.ScenariosDirect, "test/parity/scenarios/cli/01-cli.toml") {
		t.Errorf("01-cli should not be in direct scenarios: %v", p.ScenariosDirect)
	}
	if !slices.Contains(p.ScenariosClosure, "test/parity/scenarios/cli/01-cli.toml") {
		t.Errorf("01-cli should be in closure scenarios: %v", p.ScenariosClosure)
	}
	if !slices.Contains(p.ScenariosClosure, "test/parity/scenarios/unmapped/01-noedge.toml") {
		t.Errorf("unmapped scenario without PORT_MAP edge must always be selected on cmd/pig code change")
	}

	// Referenced parity fixture selects only the referencing scenario.
	pFix := Select([]string{"test/parity/testdata/openai-probe.mjs"}, g, DefaultRules, opts)
	if !slices.Equal(pFix.ScenariosClosure, []string{"test/parity/scenarios/providers/01-openai.toml"}) {
		t.Errorf("expected only 01-openai for openai-probe.mjs, got %v", pFix.ScenariosClosure)
	}

	// Unreferenced parity fixture fails closed to all scenarios.
	pUnref := Select([]string{"test/parity/testdata/mystery-fixture.mjs"}, g, DefaultRules, opts)
	if len(pUnref.ScenariosClosure) != len(g.ScenarioCovers) {
		t.Errorf("unreferenced parity fixture must select all %d scenarios, got %d", len(g.ScenarioCovers), len(pUnref.ScenariosClosure))
	}
}

func TestCIPlanScriptHookModes(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "automation", "ci", "ci-plan.sh")

	runScript := func(extraEnv []string, args ...string) (string, error) {
		cmd := exec.CommandContext(t.Context(), "bash", append([]string{script}, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), extraEnv...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// 1. Default (unset) and PIG_CI_DAG=off produce byte-identical outputs.
	outDefault, err := runScript(nil, "pull_request", "queue", "false")
	if err != nil {
		t.Fatalf("default ci-plan.sh failed: %v\n%s", err, outDefault)
	}
	outOff, err := runScript([]string{"PIG_CI_DAG=off"}, "pull_request", "queue", "false")
	if err != nil {
		t.Fatalf("PIG_CI_DAG=off ci-plan.sh failed: %v\n%s", err, outOff)
	}
	if outDefault != outOff {
		t.Fatalf("PIG_CI_DAG=off diverged from default:\ndefault:\n%s\noff:\n%s", outDefault, outOff)
	}

	// 2. Invalid PIG_CI_DAG value fails cleanly.
	if outBad, err := runScript([]string{"PIG_CI_DAG=bogus"}, "pull_request", "queue", "false"); err == nil {
		t.Fatalf("expected invalid PIG_CI_DAG to fail, got:\n%s", outBad)
	}

	// Pre-build cidag once in a temp dir so shadow/on tests stay fast and hermetic.
	bin := filepath.Join(t.TempDir(), "cidag")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cmd/cidag")
	build.Dir = filepath.Join(root, "automation", "ci", "cidag")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cidag: %v\n%s", err, out)
	}

	// 3. Shadow mode writes ci-plan.json without altering a single byte of stdout.
	tmp := t.TempDir()
	filesList := filepath.Join(tmp, "files.txt")
	planOut := filepath.Join(tmp, "ci-plan.json")
	if err := os.WriteFile(filesList, []byte("README.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outShadow, err := runScript([]string{
		"PIG_CI_DAG=shadow",
		"PIG_CI_DAG_BIN=" + bin,
		"PIG_CI_DAG_FILES_FROM=" + filesList,
		"PIG_CI_DAG_PLAN_OUT=" + planOut,
	}, "pull_request", "queue", "false")
	if err != nil {
		t.Fatalf("PIG_CI_DAG=shadow failed: %v\n%s", err, outShadow)
	}
	if outShadow != outDefault {
		t.Fatalf("PIG_CI_DAG=shadow altered GITHUB_OUTPUT:\nwant:\n%s\ngot:\n%s", outDefault, outShadow)
	}
	rawPlan, err := os.ReadFile(planOut)
	if err != nil {
		t.Fatalf("shadow mode did not write ci-plan.json: %v", err)
	}
	var parsed Plan
	if err := json.Unmarshal(rawPlan, &parsed); err != nil {
		t.Fatalf("unmarshal ci-plan.json: %v", err)
	}
	if !parsed.DocsOnly || parsed.Mode != ModeShadow {
		t.Errorf("unexpected shadow plan: %+v", parsed)
	}

	// 4. On mode with a docs-only or changelog-fragment file prunes non-gate PR lanes
	//    while preserving global gates (build, checks, contracts) and non-empty linux matrix.
	if err := os.WriteFile(filesList, []byte("changelog.d/sample.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Use a fake graphless short-circuit? No, changelog.d/sample.md triggers LoadGraph,
	// so verify on-mode fail-closed fallback (no base/files) AND on-mode with ForceFull (merge_group).
	outMergeOn, err := runScript([]string{
		"PIG_CI_DAG=on",
		"PIG_CI_DAG_BIN=" + bin,
		"PIG_CI_DAG_FILES_FROM=" + filesList,
	}, "merge_group", "queue", "false")
	if err != nil {
		t.Fatalf("PIG_CI_DAG=on merge_group failed: %v\n%s", err, outMergeOn)
	}
	outMergeDefault, err := runScript(nil, "merge_group", "queue", "false")
	if err != nil {
		t.Fatalf("default merge_group failed: %v\n%s", err, outMergeDefault)
	}
	if outMergeOn != outMergeDefault {
		t.Fatalf("PIG_CI_DAG=on must run full on merge_group identically to static plan:\nwant:\n%s\ngot:\n%s", outMergeDefault, outMergeOn)
	}

	// 5. On mode without PIG_CI_DAG_BASE or PIG_CI_DAG_FILES_FROM fails closed to static plan.
	outFallback, err := runScript([]string{"PIG_CI_DAG=on", "PIG_CI_DAG_BIN=" + bin}, "pull_request", "queue", "false")
	if err != nil {
		t.Fatalf("PIG_CI_DAG=on fallback failed: %v\n%s", err, outFallback)
	}
	if !strings.Contains(outFallback, "full=false\n") || !strings.Contains(outFallback, "linux=[") {
		t.Fatalf("unexpected fallback output:\n%s", outFallback)
	}
}

func TestRuleMatchedEmbeddedFilePreservesReverseDependents(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	// Simulate coding/extension/host/subprocess embedding runtime-node.pack and coding/cli importing subprocess.
	subPkg := DefaultModule + "/coding/extension/host/subprocess"
	cliPkg := DefaultModule + "/coding/cli"
	g.EmbedSrc["coding/extension/host/subprocess/runtime-node.pack"] = []string{subPkg}
	g.RevDeps[subPkg] = []string{cliPkg}

	plan := Select([]string{"coding/extension/host/subprocess/runtime-node.pack"}, g, DefaultRules, Options{ReadFile: readFromMap(sources)})
	if plan.Full {
		t.Fatalf("rule-matched embedded file unexpectedly failed closed to full: %v", plan.FullReason)
	}
	if !slices.Contains(plan.CompilePackages, "./coding/extension/host/subprocess") {
		t.Errorf("expected ./coding/extension/host/subprocess in compile packages, got %v", plan.CompilePackages)
	}
	if !slices.Contains(plan.AffectedPackages, "./coding/cli") {
		t.Errorf("expected reverse dependent ./coding/cli in affected packages, got %v", plan.AffectedPackages)
	}
	// Explicit rule lanes remain unioned with graph lanes.
	for _, wantLane := range []string{"node-runtime", "node-loader", "test-cli-1", "win-cli"} {
		if !slices.Contains(plan.Lanes, wantLane) {
			t.Errorf("expected lane %s in unioned lanes %v", wantLane, plan.Lanes)
		}
	}

	// A rule-matched non-Go file with NO embed edges stays exempt from the unowned check.
	nonEmbedPlan := Select([]string{"coding/extension/host/subprocess/runtime-node/index.mjs"}, g, DefaultRules, Options{ReadFile: readFromMap(sources)})
	if nonEmbedPlan.Full || len(nonEmbedPlan.Unowned) > 0 {
		t.Fatalf("rule-owned non-embedded file should stay exempt from unowned check: full=%v unowned=%v", nonEmbedPlan.Full, nonEmbedPlan.Unowned)
	}
}

func TestPartiallyMappedParityScenarioIsUnprovable(t *testing.T) {
	g, sources := buildFixtureGraph(t)
	// Scenario covers BOTH a mapped upstream file (cli.ts -> coding/cli/cli.go) and an unmapped one.
	// Changing ai/openai.go changes cmd/pig closure; the scenario must be selected even in direct/dir tiers
	// because its unmapped covers path cannot be proven unaffected.
	mixedScen := "test/parity/scenarios/mixed/01-partial.toml"
	g.ScenarioCovers[mixedScen] = []string{
		"packages/coding-agent/src/cli.ts",
		"packages/unknown/src/unmapped.ts",
	}
	g.ScenarioTexts[mixedScen] = `name = "01-partial"`

	p := Select([]string{"ai/openai.go"}, g, DefaultRules, Options{ReadFile: readFromMap(sources)})
	if !slices.Contains(p.ScenariosDirect, mixedScen) || !slices.Contains(p.ScenariosDir, mixedScen) || !slices.Contains(p.ScenariosClosure, mixedScen) {
		t.Fatalf("partially mapped scenario %s must be conservatively selected across all tiers; direct=%v dir=%v closure=%v",
			mixedScen, p.ScenariosDirect, p.ScenariosDir, p.ScenariosClosure)
	}
}

func TestRenderCIPlanFullFallbackPromotedQueueAndUnknownCatalogueRow(t *testing.T) {
	rows := []TierRow{
		{Job: "linux", Shard: "build", Tier: TierPR, LightTarget: "ci-build-pr"},
		{Job: "linux", Shard: "checks", Tier: TierPR, LightTarget: "ci-checks-pr"},
		{Job: "linux", Shard: "contracts", Tier: TierPR},
		{Job: "linux", Shard: "test-subprocess", Tier: TierPR},
		{Job: "windows", Shard: "native-1", Tier: TierQueue},
		{Job: "windows", Shard: "native-5", Tier: TierQueue}, // passes table validation, absent from LaneCatalogue
		{Job: "macos", Shard: "native", Tier: TierQueue},
		{Job: "single", Shard: "node-runtime", Tier: TierQueue},
		{Job: "single", Shard: "chord", Tier: TierQueue},
	}

	// 1. Active DAG full fallback (e.g. unowned file on light PR) must render Full=true with queue rows enabled.
	fullFallbackPlan := &Plan{Full: true, FullReason: []string{"unowned:mystery.bin"}}
	fallbackOut, err := RenderCIPlan(rows, "pull_request", "queue", false, fullFallbackPlan)
	if err != nil {
		t.Fatalf("RenderCIPlan full fallback: %v", err)
	}
	if !fallbackOut.Full {
		t.Fatalf("expected Full=true when active dagPlan.Full is true")
	}
	if len(fallbackOut.Windows) != 2 || len(fallbackOut.MacOS) != 1 {
		t.Fatalf("expected queue windows/macos rows to run on full fallback, got win=%v mac=%v", fallbackOut.Windows, fallbackOut.MacOS)
	}
	for _, s := range fallbackOut.Singles {
		if !s.Run {
			t.Errorf("expected single %s to run on full fallback", s.Name)
		}
	}

	// 2. Active light PR plan where PRLanes promotes queue-tier node-runtime must enable run-node-runtime=true
	//    while keeping unpromoted queue-tier chord=false.
	promotedPlan := &Plan{
		Full:    false,
		Lanes:   []string{"preflight", "build", "checks", "contracts", "test-subprocess", "node-runtime"},
		PRLanes: []string{"preflight", "build", "checks", "contracts", "test-subprocess", "node-runtime"},
	}
	promoOut, err := RenderCIPlan(rows, "pull_request", "queue", false, promotedPlan)
	if err != nil {
		t.Fatalf("RenderCIPlan promoted queue row: %v", err)
	}
	if promoOut.Full {
		t.Fatalf("expected Full=false for selective light PR plan")
	}
	singleRuns := map[string]bool{}
	for _, s := range promoOut.Singles {
		singleRuns[s.Name] = s.Run
	}
	if !singleRuns["node-runtime"] {
		t.Errorf("expected DAG-promoted queue single node-runtime to run, got singles=%+v", promoOut.Singles)
	}
	if singleRuns["chord"] {
		t.Errorf("expected unpromoted queue single chord to remain false, got singles=%+v", promoOut.Singles)
	}

	// 3. Active full-tier PR plan (lightMode="") must keep windows | native-5 | queue because win-native-5
	//    has no LaneCatalogue entry (fail closed), while pruning known native-1 when not in allowedLanes.
	fullTierOut, err := RenderCIPlan(rows, "pull_request", "", false, promotedPlan)
	if err != nil {
		t.Fatalf("RenderCIPlan unknown catalogue row: %v", err)
	}
	var winShards []string
	for _, w := range fullTierOut.Windows {
		winShards = append(winShards, w.Shard)
	}
	if !slices.Contains(winShards, "native-5") {
		t.Errorf("expected unmapped catalogue row native-5 to stay enabled, got windows=%v", winShards)
	}
	if slices.Contains(winShards, "native-1") {
		t.Errorf("expected known unallowed lane native-1 to be pruned, got windows=%v", winShards)
	}
}

func TestDecodeListPackagesLoadErrorsVsPlatformExclusion(t *testing.T) {
	// Real load error (Error field) fails closed.
	errJSON := `{"ImportPath":"example.com/bad","Dir":"/repo/bad","Incomplete":true,"Error":{"Err":"broken syntax"}}`
	if _, err := decodeListPackages([]byte(errJSON), "linux"); err == nil {
		t.Fatalf("expected decodeListPackages to fail on package Error")
	}

	// Dependency load error (DepsErrors) fails closed.
	depsErrJSON := `{"ImportPath":"example.com/a","Dir":"/repo/a","Incomplete":true,"DepsErrors":[{"Err":"missing dep"}]}`
	if _, err := decodeListPackages([]byte(depsErrJSON), "linux"); err == nil {
		t.Fatalf("expected decodeListPackages to fail on DepsErrors")
	}

	// Incomplete without platform exclusion fails closed.
	incompleteJSON := `{"ImportPath":"example.com/a","Dir":"/repo/a","Incomplete":true}`
	if _, err := decodeListPackages([]byte(incompleteJSON), "linux"); err == nil {
		t.Fatalf("expected decodeListPackages to fail on Incomplete metadata")
	}

	// Platform build-constraint exclusion is allowed (not a load failure).
	exclJSON := `{"ImportPath":"example.com/winonly","Dir":"/repo/winonly","Incomplete":true,"Error":{"Err":"build constraints exclude all Go files in /repo/winonly"}}`
	pkgs, err := decodeListPackages([]byte(exclJSON), "linux")
	if err != nil {
		t.Fatalf("platform exclusion should not fail decodeListPackages: %v", err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("expected 1 decoded package, got %d", len(pkgs))
	}
}

func TestCIPlanScriptResolvesRelativePathsBeforeCD(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	// Pre-build cidag once, then also verify relative path resolution when invoked from repo root
	// using a relative script path and relative tier table / files-from / plan-out / summary paths,
	// plus the "-" stdin sentinel.
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "cidag")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cmd/cidag")
	build.Dir = filepath.Join(root, "automation", "ci", "cidag")
	build.Env = append(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build cidag: %v\n%s", err, out)
	}

	// Create a caller directory distinct from root with relative files.
	callerDir := t.TempDir()
	relFiles := "rel-files.txt"
	relPlan := "out/rel-plan.json"
	relSummary := "rel-summary.md"
	relTiers := "rel-tiers.txt"
	if err := os.WriteFile(filepath.Join(callerDir, relFiles), []byte("README.md\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tiersBytes, err := os.ReadFile(filepath.Join(root, "automation", "ci", "ci-tiers.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(callerDir, relTiers), tiersBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	scriptPath := filepath.Join(root, "automation", "ci", "ci-plan.sh")
	cmd := exec.CommandContext(t.Context(), "bash", scriptPath, "pull_request", "queue", "false", relTiers)
	cmd.Dir = callerDir
	cmd.Env = append(os.Environ(),
		"PIG_CI_DAG=on",
		"PIG_CI_DAG_BIN="+bin,
		"PIG_CI_DAG_FILES_FROM="+relFiles,
		"PIG_CI_DAG_PLAN_OUT="+relPlan,
		"GITHUB_STEP_SUMMARY="+relSummary,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-plan.sh with relative paths failed: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "selector failed") {
		t.Fatalf("ci-plan.sh fell back to static plan instead of resolving relative paths:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(callerDir, relPlan)); err != nil {
		t.Fatalf("expected relative plan-out written in caller dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(callerDir, relSummary)); err != nil {
		t.Fatalf("expected relative summary written in caller dir: %v", err)
	}

	// Verify "-" stdin sentinel is preserved.
	stdinCmd := exec.CommandContext(t.Context(), "bash", scriptPath, "pull_request", "queue", "false", relTiers)
	stdinCmd.Dir = callerDir
	stdinCmd.Stdin = strings.NewReader("README.md\n")
	stdinCmd.Env = append(os.Environ(),
		"PIG_CI_DAG=on",
		"PIG_CI_DAG_BIN="+bin,
		"PIG_CI_DAG_FILES_FROM=-",
	)
	stdinOut, err := stdinCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-plan.sh with stdin sentinel failed: %v\n%s", err, stdinOut)
	}
	if strings.Contains(string(stdinOut), "selector failed") {
		t.Fatalf("ci-plan.sh stdin sentinel fell back:\n%s", stdinOut)
	}

	// Verify invoking by relative script path from repo root without PIG_CI_DAG_BIN (go run launcher)
	// resolves default relative tier table automation/ci/ci-tiers.txt before cd into automation/ci/cidag.
	goRunCmd := exec.CommandContext(t.Context(), "bash", "automation/ci/ci-plan.sh", "pull_request", "queue", "false")
	goRunCmd.Dir = root
	goRunCmd.Stdin = strings.NewReader("README.md\n")
	goRunCmd.Env = append(os.Environ(),
		"PIG_CI_DAG=on",
		"PIG_CI_DAG_FILES_FROM=-",
	)
	goRunOut, err := goRunCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ci-plan.sh via go run launcher failed: %v\n%s", err, goRunOut)
	}
	if strings.Contains(string(goRunOut), "selector failed") {
		t.Fatalf("ci-plan.sh go run launcher failed to resolve default relative tier table:\n%s", goRunOut)
	}
}
