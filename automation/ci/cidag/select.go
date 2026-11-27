// SPDX-License-Identifier: MIT

package cidag

import (
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

var (
	// shardedHalf1 mirrors test-grouped.sh fast-sharded 1 2.
	shardedHalf1 = map[string]bool{
		"./ai":                true,
		"./tui":               true,
		"./tui/internal/hljs": true,
	}
	// shardedHalf2 mirrors test-grouped.sh fast-sharded 2 2.
	shardedHalf2 = map[string]bool{
		"./coding":               true,
		"./internal/codingagent": true,
		"./test/parity/interface-closure/autobind": true,
		"./test/parity/cmd/portmapcheck":           true,
		"./codemode":                               true,
		"./durable/codemode":                       true,
	}
	heavyPackages = map[string]bool{
		"./coding/cli":                       true,
		"./coding/extension/host/subprocess": true,
		"./test/extension-conformance":       true,
	}
	allocRatchetPackages = map[string]bool{
		"./agent":                            true,
		"./ai":                               true,
		"./ai/providers/bedrock":             true,
		"./coding":                           true,
		"./coding/cli":                       true,
		"./coding/extension/host/inproc":     true,
		"./coding/extension/host/subprocess": true,
		"./internal/codingagent":             true,
		"./internal/sessionentry":            true,
		"./tui":                              true,
		"./tui/widthx":                       true,
	}
	osPromoteByCompilePkg = map[string][]string{
		"./coding/cli":                        {"win-cli", "mac-cli"},
		"./coding/extension/host/subprocess":  {"win-ext-host", "mac-ext-host"},
		"./coding/extension/host/runtimecell": {"win-ext-host", "mac-ext-host"},
		"./test/extension-conformance":        {"win-ext-conformance"},
	}
)

// Select computes the fail-closed affected-lane and affected-package plan for
// files against g and rules (defaults to DefaultRules when nil).
//
// Invariants (formally proved in automation/ci/cidag/proof/SelectionSoundness.lean):
//   - Monotonicity: S1 ⊆ S2 => plan(S1) ⊆ plan(S2).
//   - Transitive Closure Soundness: modified compile-edge package p => RevClosure(p) ⊆ affected.
//   - Fail-Closed Completeness: any unowned file, |S| > MaxFiles, or missing graph => Full = true.
//   - Global-Invariant Lock-In: every non-docs-only change keeps GlobalGateLanes and GlobalGuardPackages.
//   - Tiering Preservation: PRLanes ⊆ Lanes.
func Select(files []string, g *Graph, rules []PathRule, opts Options) Plan {
	t0 := time.Now()
	if rules == nil {
		rules = DefaultRules
	}
	maxFiles := opts.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}

	// Canonicalize and deduplicate input file list.
	cleanedSet := make(map[string]bool, len(files))
	for _, f := range files {
		if n := normalizePath(f); n != "" {
			cleanedSet[n] = true
		}
	}
	cleanFiles := sortedKeys(cleanedSet)

	plan := Plan{
		Selector:    "cidag/v1",
		Mode:        opts.Mode,
		Event:       opts.Event,
		Base:        opts.Base,
		Head:        opts.Head,
		Files:       cleanFiles,
		FileCount:   len(cleanFiles),
		FullMinutes: math.Round(FullMinutes()*10) / 10,
	}

	makeFull := func(reasons []string, unowned []string) Plan {
		plan.Full = true
		plan.DocsOnly = false
		plan.FullReason = reasons
		plan.Unowned = unowned
		plan.Lanes = AllLanes()
		plan.PRLanes = AllLanes()
		plan.GuardPackages = append([]string(nil), GlobalGuardPackages...)
		if len(reasons) > 0 {
			plan.Why = map[string][]string{"ALL": reasons}
		}
		plan.Minutes = plan.FullMinutes
		plan.PRMinutes = plan.FullMinutes
		plan.SelectDurationMS = roundMS(time.Since(t0))
		return plan
	}

	if opts.ForceFull {
		reason := opts.ForceFullReason
		if reason == "" {
			reason = "forced full run"
		}
		return makeFull([]string{reason}, nil)
	}

	if len(cleanFiles) > maxFiles {
		return makeFull([]string{fmt.Sprintf("%d files > %d", len(cleanFiles), maxFiles)}, nil)
	}

	if len(cleanFiles) == 0 {
		plan.DocsOnly = true
		plan.Lanes = nil
		plan.PRLanes = nil
		plan.SelectDurationMS = roundMS(time.Since(t0))
		return plan
	}

	if g == nil {
		return makeFull([]string{"go package graph unavailable"}, nil)
	}

	lanes := map[string]bool{}
	ruleLanes := map[string]bool{}
	graphLanes := map[string]bool{}
	whyMap := map[string]map[string]bool{}
	add := func(ls []string, reason string, fromGraph bool) {
		for _, l := range ls {
			if _, ok := LookupLane(l); !ok {
				continue
			}
			lanes[l] = true
			if whyMap[l] == nil {
				whyMap[l] = map[string]bool{}
			}
			whyMap[l][reason] = true
			if fromGraph {
				graphLanes[l] = true
			} else {
				ruleLanes[l] = true
			}
		}
	}

	full := false
	fullReasons := map[string]bool{}
	nonDocs := false
	var goCandidates []string

	for _, f := range cleanFiles {
		rule, matched := MatchRule(f, rules)
		if matched {
			switch rule.Kind {
			case RuleFull:
				full = true
				nonDocs = true
				fullReasons[f] = true
			case RuleDocs:
				// Project prose / metadata: no product lanes.
			case RuleLanes:
				nonDocs = true
				add(rule.Lanes, f, false)
			}
			continue
		}
		nonDocs = true
		goCandidates = append(goCandidates, f)
	}

	// Classify all non-rule files through the Go graph (compile vs test-only vs unowned).
	// Also scan rule-matched .go files (such as protocol*.go or runtime_node_digest_generated.go)
	// and rule-matched non-Go files with known embed edges (such as runtime-node.pack)
	// so their compile reverse closure and affected packages stay sound while keeping
	// rule-owned files without embed edges exempt from the unowned check.
	goInputs := append([]string(nil), goCandidates...)
	for _, f := range cleanFiles {
		if _, matched := MatchRule(f, rules); matched {
			if strings.HasSuffix(f, ".go") || len(g.EmbedSrc[f]) > 0 || len(g.EmbedTest[f]) > 0 {
				goInputs = append(goInputs, f)
			}
		}
	}

	compilePkgs, testOnlyPkgs, unowned := classifyGoFiles(goCandidates, goInputs, g)

	// Nested module go.mod/go.sum outside root triggers test-fast + toolchains (test-go-modules).
	for _, f := range cleanFiles {
		base := path.Base(f)
		if (base == "go.mod" || base == "go.sum") && f != "go.mod" && f != "go.sum" {
			nonDocs = true
			add([]string{"test-fast", "toolchains"}, "nested-module:"+f, false)
			unowned = slices.DeleteFunc(unowned, func(u string) bool { return u == f })
		}
	}

	if len(unowned) > 0 {
		full = true
		for _, u := range unowned {
			fullReasons["unowned:"+u] = true
		}
	}

	// Transitive reverse closure of compile-edge packages union test-only packages.
	affected := g.ReverseClosure(compilePkgs)
	for p := range testOnlyPkgs {
		affected[p] = true
	}

	// Global-invariant lock-in (CI-DAG-DESIGN.md §2.4, §4.1):
	// Every non-docs-only change keeps all gate lanes and tree-wide guard packages.
	if nonDocs {
		add(GlobalGateLanes, "global-gate-invariant", false)
		for _, guard := range GlobalGuardPackages {
			fullGuard := g.Module + "/" + strings.TrimPrefix(guard, "./")
			if g.MainPkgs[fullGuard] {
				affected[fullGuard] = true
			}
		}
		plan.GuardPackages = append([]string(nil), GlobalGuardPackages...)
	}

	// Map affected Go packages to CI lanes.
	if len(compilePkgs) > 0 || len(testOnlyPkgs) > 0 {
		add([]string{"portmap", "android"}, "go-change", true)
		shortAffected := make(map[string]bool, len(affected))
		for p := range affected {
			shortAffected[g.ShortPkg(p)] = true
		}

		hasFast := false
		for sp := range shortAffected {
			switch {
			case shardedHalf1[sp]:
				add([]string{"test-fast-sharded"}, "sharded-1:"+sp, true)
			case shardedHalf2[sp]:
				add([]string{"test-fast-sharded-2"}, "sharded-2:"+sp, true)
			case heavyPackages[sp]:
				// handled by dedicated lanes below
			default:
				hasFast = true
			}
			if allocRatchetPackages[sp] {
				add([]string{"alloc-ratchet"}, "alloc-baseline:"+sp, true)
			}
		}
		if hasFast {
			add([]string{"test-fast"}, "fast-pkg", true)
		}
		if shortAffected["./coding/cli"] {
			add([]string{"test-cli-1", "test-cli-2", "win-cli", "mac-cli"}, "coding/cli", true)
		}
		if shortAffected["./coding/extension/host/subprocess"] || shortAffected["./coding/extension/host/runtimecell"] {
			add([]string{"test-subprocess", "win-ext-host", "mac-ext-host", "node-runtime", "node-loader"}, "subprocess", true)
		}
		if shortAffected["./test/extension-conformance"] {
			add([]string{"test-conformance", "win-ext-conformance"}, "extension-conformance", true)
		}
		if shortAffected["./env"] || shortAffected["./coding/piglet"] || shortAffected["./chord/nextnode/plugins"] {
			add([]string{"mac-ext-host"}, "macos-packages", true)
		}

		// Windows native shards (partition matches automation/ci/windows-native-shard.sh).
		for p := range g.OSTests["windows"] {
			if !affected[p] {
				continue
			}
			sp := g.ShortPkg(p)
			switch sp {
			case "./coding/cli", "./coding/extension/host/subprocess", "./coding/extension/host/runtimecell", "./test/extension-conformance":
				continue
			case "./coding", "./tui":
				add([]string{"win-native-1"}, "win-native:"+sp, true)
			case "./ai":
				add([]string{"win-native-2"}, "win-native:"+sp, true)
			case "./internal/codingagent":
				add([]string{"win-native-3"}, "win-native:"+sp, true)
			default:
				add([]string{"win-native-4"}, "win-native:"+sp, true)
			}
		}
		// macOS native tests: workflow runs ./ai ./internal/codingagent/tools ./internal/nativeplatform ./tui.
		for _, sp := range []string{"./ai", "./internal/codingagent/tools", "./internal/nativeplatform", "./tui"} {
			if shortAffected[sp] {
				add([]string{"mac-native"}, "mac-native:"+sp, true)
			}
		}
		for p := range g.OSTests["darwin"] {
			if affected[p] {
				add([]string{"mac-native"}, "mac-native:"+g.ShortPkg(p), true)
			}
		}

		// Sub-system single jobs and toolchains.
		for sp := range shortAffected {
			if strings.HasPrefix(sp, "./tui") || strings.HasPrefix(sp, "./coding") || strings.HasPrefix(sp, "./internal/codingagent") || sp == "./cmd/pig" {
				add([]string{"toolchains"}, "race-or-integration:"+sp, true)
			}
			if strings.HasPrefix(sp, "./durable") {
				add([]string{"durable-cf-host", "durable-contract"}, "durable:"+sp, true)
			}
			if strings.HasPrefix(sp, "./chord") {
				add([]string{"chord", "durable-cf-host"}, "chord:"+sp, true)
			}
		}
	}

	// Parity scenario selection (direct / dir / closure tiers).
	scenClosure, scenDir, scenDirect := selectParityScenarios(cleanFiles, compilePkgs, affected, g)
	if len(scenClosure) > 0 {
		add([]string{"parity-1", "parity-2"}, fmt.Sprintf("%d scenarios", len(scenClosure)), true)
	}

	// OS-specific file detection and OS-sensitivity flags.
	osSpecific := false
	for _, f := range cleanFiles {
		if isOSSpecificFile(f, g, opts.ReadFile) {
			osSpecific = true
			break
		}
	}
	compileOSNarrow := false
	compileOSBroad := false
	for p := range compilePkgs {
		if g.OSNarrow[p] {
			compileOSNarrow = true
		}
		if g.OSBroad[p] {
			compileOSBroad = true
		}
	}

	plan.DocsOnly = !nonDocs
	plan.OSSpecific = osSpecific
	plan.CompileOSNarrow = compileOSNarrow
	plan.CompileOSBroad = compileOSBroad
	plan.CompilePackages = shortSorted(compilePkgs, g)
	plan.TestOnlyPackages = shortSorted(testOnlyPkgs, g)
	plan.AffectedPackages = shortSorted(affected, g)
	plan.ScenariosClosure = scenClosure
	plan.ScenariosDir = scenDir
	plan.ScenariosDirect = scenDirect
	plan.Unowned = unowned
	plan.GraphLanes = sortedKeys(graphLanes)

	if full {
		plan.Full = true
		plan.FullReason = sortedKeys(fullReasons)
		plan.Lanes = AllLanes()
		plan.PRLanes = AllLanes()
		plan.Why = flattenWhy(whyMap)
		if plan.Why == nil {
			plan.Why = map[string][]string{}
		}
		plan.Why["ALL"] = plan.FullReason
		plan.Minutes = plan.FullMinutes
		plan.PRMinutes = plan.FullMinutes
		plan.SelectDurationMS = roundMS(time.Since(t0))
		return plan
	}

	selectedLanes := sortedKeys(lanes)
	prLanes := computePRLanes(selectedLanes, ruleLanes, compilePkgs, osSpecific, g)

	plan.Lanes = selectedLanes
	plan.PRLanes = prLanes
	plan.Why = flattenWhy(whyMap)
	plan.Minutes = math.Round(LaneMinutes(selectedLanes)*10) / 10
	plan.PRMinutes = math.Round(LaneMinutes(prLanes)*10) / 10
	plan.SelectDurationMS = roundMS(time.Since(t0))
	return plan
}

// classifyGoFiles separates compile edges (reverse closure runs) from test-only
// edges (package alone) and reports files unowned by the Go graph.
func classifyGoFiles(unmatched, allGoInputs []string, g *Graph) (compilePkgs, testOnlyPkgs map[string]bool, unowned []string) {
	compilePkgs = map[string]bool{}
	testOnlyPkgs = map[string]bool{}
	unmatchedSet := make(map[string]bool, len(unmatched))
	for _, f := range unmatched {
		unmatchedSet[f] = true
	}

	seen := map[string]bool{}
	for _, f := range allGoInputs {
		if seen[f] {
			continue
		}
		seen[f] = true

		// Always propagate known //go:embed edges regardless of file suffix or path-rule
		// ownership so reverse dependents of embedded assets (including embedded .go
		// fixtures) stay in the compile closure.
		hit := false
		if pkgs, ok := g.EmbedSrc[f]; ok && len(pkgs) > 0 {
			for _, p := range pkgs {
				compilePkgs[p] = true
			}
			hit = true
		}
		if pkgs, ok := g.EmbedTest[f]; ok && len(pkgs) > 0 {
			for _, p := range pkgs {
				testOnlyPkgs[p] = true
			}
			hit = true
		}

		if strings.HasSuffix(f, ".go") {
			// S4: a Go file excluded under every queried GOOS (not //go:build ignore) is unowned -> full.
			if g.IgnoredAll[f] && unmatchedSet[f] && !hit {
				unowned = append(unowned, f)
				continue
			}
			dir := path.Dir(f)
			if dir == "" {
				dir = "."
			}
			pkg, ok := g.ByDir[dir]
			if !ok {
				if unmatchedSet[f] && !hit {
					unowned = append(unowned, f)
				}
				continue
			}
			if strings.HasSuffix(f, "_test.go") {
				testOnlyPkgs[pkg] = true
			} else {
				compilePkgs[pkg] = true
			}
			continue
		}
		// Remaining heuristics (string references, testdata, unowned fallback) apply
		// only to non-Go files not already owned by a path rule.
		if !unmatchedSet[f] {
			continue
		}
		if refSrc := g.Referencing(f, g.SrcFiles); len(refSrc) > 0 {
			for p := range refSrc {
				compilePkgs[p] = true
			}
			hit = true
		}
		if refTest := g.Referencing(f, g.TestFiles); len(refTest) > 0 {
			for p := range refTest {
				testOnlyPkgs[p] = true
			}
			hit = true
		}
		if strings.Contains("/"+f, "/testdata/") {
			if own, ok := g.OwnerDir(f); ok {
				testOnlyPkgs[own] = true
				hit = true
			}
		}
		if !hit {
			unowned = append(unowned, f)
		}
	}
	slices.Sort(unowned)
	return compilePkgs, testOnlyPkgs, unowned
}

// selectParityScenarios computes closure, dir, and direct scenario sets following
// CI-DAG-DESIGN.md §2.2-2.3.
func selectParityScenarios(files []string, compilePkgs, affected map[string]bool, g *Graph) (closure, dirTier, direct []string) {
	if len(g.ScenarioCovers) == 0 {
		return nil, nil, nil
	}
	pigPkg := g.Module + "/cmd/pig"
	pigClosure := g.ForwardClosure(pigPkg)

	codeHit := map[string]bool{}
	if len(compilePkgs) > 0 {
		for p := range affected {
			if pigClosure[p] {
				codeHit[p] = true
			}
		}
	}

	affDirs := map[string]bool{}
	for p := range codeHit {
		sp := g.ShortPkg(p)
		if sp == "." {
			affDirs["."] = true
		} else {
			affDirs[strings.TrimPrefix(sp, "./")] = true
		}
	}

	changedProd := map[string]bool{}
	changedProdDirs := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, ".go") && !strings.HasSuffix(f, "_test.go") {
			changedProd[f] = true
			d := path.Dir(f)
			if d == "" {
				d = "."
			}
			changedProdDirs[d] = true
		}
	}

	scenClosure := map[string]bool{}
	scenDir := map[string]bool{}
	scenDirect := map[string]bool{}

	for scen, ups := range g.ScenarioCovers {
		mappedFiles := map[string]bool{}
		mappedDirs := map[string]bool{}
		for _, up := range ups {
			for _, gf := range g.PortMapEdges[up] {
				mappedFiles[gf] = true
				d := path.Dir(gf)
				if d == "" {
					d = "."
				}
				mappedDirs[d] = true
			}
		}
		unprovable := len(mappedFiles) == 0
		for _, up := range ups {
			if len(g.PortMapEdges[up]) == 0 {
				unprovable = true
				break
			}
		}
		if unprovable {
			// Any covers path lacking a PORT_MAP edge makes the scenario unprovable;
			// keep it selected whenever cmd/pig code changed.
			if len(codeHit) > 0 {
				scenClosure[scen] = true
				scenDir[scen] = true
				scenDirect[scen] = true
			}
			continue
		}
		if len(codeHit) == 0 {
			continue
		}
		for mf := range mappedFiles {
			if changedProd[mf] {
				scenDirect[scen] = true
				break
			}
		}
		for md := range mappedDirs {
			if changedProdDirs[md] {
				scenDir[scen] = true
			}
			if affDirs[md] {
				scenClosure[scen] = true
			}
		}
		// Maintain lattice inclusion: direct ⊆ dir ⊆ closure.
		if scenDirect[scen] {
			scenDir[scen] = true
		}
		if scenDir[scen] {
			scenClosure[scen] = true
		}
	}

	// Direct scenario or parity testdata edits.
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, "test/parity/scenarios/"):
			if _, ok := g.ScenarioCovers[f]; ok {
				scenClosure[f] = true
				scenDir[f] = true
				scenDirect[f] = true
			} else {
				parent := path.Dir(f)
				grandparent := path.Dir(parent)
				for scen := range g.ScenarioCovers {
					sd := path.Dir(scen)
					if sd == parent || sd == grandparent {
						scenClosure[scen] = true
						scenDir[scen] = true
						scenDirect[scen] = true
					}
				}
			}
		case strings.HasPrefix(f, "test/parity/testdata/"):
			parts := strings.Split(f, "/")
			key := f
			if len(parts) >= 4 {
				key = strings.Join(parts[:4], "/")
			}
			base := path.Base(f)
			matchedAny := false
			for scen, text := range g.ScenarioTexts {
				if strings.Contains(text, key) || strings.Contains(text, base) {
					scenClosure[scen] = true
					scenDir[scen] = true
					scenDirect[scen] = true
					matchedAny = true
				}
			}
			if !matchedAny {
				// Fail closed for an unreferenced parity fixture: select all scenarios.
				for scen := range g.ScenarioCovers {
					scenClosure[scen] = true
					scenDir[scen] = true
					scenDirect[scen] = true
				}
			}
		}
	}

	return sortedKeys(scenClosure), sortedKeys(scenDir), sortedKeys(scenDirect)
}

// computePRLanes projects selectedLanes onto the PR tier plus monotone positive
// promotions (explicit rule-selected cross-language/runtime lanes, OS-promoted
// compile packages, and OS-specific files). Every condition is positive in S,
// preserving both Monotonicity (Theorem 1) and PRLanes ⊆ selectedLanes (Theorem 5).
func computePRLanes(selectedLanes []string, ruleLanes, compilePkgs map[string]bool, osSpecific bool, g *Graph) []string {
	selectedSet := make(map[string]bool, len(selectedLanes))
	for _, l := range selectedLanes {
		selectedSet[l] = true
	}
	keep := map[string]bool{}
	for _, l := range selectedLanes {
		spec, ok := LookupLane(l)
		if !ok {
			continue
		}
		if spec.Tier == TierPR {
			keep[l] = true
			continue
		}
		// Rule-selected cross-language / runtime lanes are promoted on push.
		// Using positive membership ruleLanes[l] (instead of !graphLanes[l]) preserves
		// monotonicity when S grows to also trigger l via the Go graph.
		if ruleLanes[l] && (strings.HasPrefix(l, "win-") || strings.HasPrefix(l, "mac-") || strings.HasPrefix(l, "node-") || strings.HasPrefix(l, "durable") || l == "chord" || l == "toolchains" || l == "test-conformance") {
			keep[l] = true
		}
	}
	for p := range compilePkgs {
		for _, promoted := range osPromoteByCompilePkg[g.ShortPkg(p)] {
			if selectedSet[promoted] {
				keep[promoted] = true
			}
		}
	}
	if osSpecific {
		for _, l := range selectedLanes {
			if strings.HasPrefix(l, "win-native") || l == "mac-native" {
				keep[l] = true
			}
		}
	}
	return sortedKeys(keep)
}

func isOSSpecificFile(relPath string, g *Graph, readOverride func(string) ([]byte, error)) bool {
	if !strings.HasSuffix(relPath, ".go") {
		return false
	}
	if osSpecificNameRE.MatchString(relPath) {
		return true
	}
	var data []byte
	var err error
	switch {
	case readOverride != nil:
		data, err = readOverride(relPath)
	case g != nil && g.Root != "":
		data, err = os.ReadFile(filepath.Join(g.Root, filepath.FromSlash(relPath)))
	default:
		return false
	}
	if err != nil {
		return false
	}
	limit := min(len(data), 600)
	for line := range strings.SplitSeq(string(data[:limit]), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//go:build") && osSpecificBuildWord.MatchString(trimmed) {
			return true
		}
	}
	return false
}

func shortSorted(pkgs map[string]bool, g *Graph) []string {
	if len(pkgs) == 0 {
		return nil
	}
	out := make([]string, 0, len(pkgs))
	for p := range pkgs {
		out = append(out, g.ShortPkg(p))
	}
	slices.Sort(out)
	return out
}

func flattenWhy(whyMap map[string]map[string]bool) map[string][]string {
	if len(whyMap) == 0 {
		return nil
	}
	out := make(map[string][]string, len(whyMap))
	for lane, reasons := range whyMap {
		sorted := sortedKeys(reasons)
		if len(sorted) > 5 {
			sorted = sorted[:5]
		}
		out[lane] = sorted
	}
	return out
}

func roundMS(d time.Duration) float64 {
	return math.Round(float64(d.Microseconds())/10.0) / 100.0
}

// AuditRun checks whether a computed Plan would have selected every lane that
// actually failed in a full CI run (shadow-mode empirical edge audit).
// tier must be "full" (default, compares against plan.Lanes) or "pr" (compares
// against plan.PRLanes).
func AuditRun(plan Plan, failingLanes []string, tier, runID, commit string) AuditReport {
	if tier == "" {
		tier = "full"
	}
	selected := plan.Lanes
	if tier == "pr" {
		selected = plan.PRLanes
	}
	selectedSet := make(map[string]bool, len(selected))
	for _, l := range selected {
		selectedSet[l] = true
	}

	failSet := map[string]bool{}
	for _, raw := range failingLanes {
		l := strings.TrimSpace(raw)
		if l != "" {
			failSet[l] = true
		}
	}
	sortedFail := sortedKeys(failSet)

	var covered, blindSpots, unknown []string
	for _, l := range sortedFail {
		if _, known := LookupLane(l); !known {
			unknown = append(unknown, l)
		}
		if plan.Full || selectedSet[l] {
			covered = append(covered, l)
		} else {
			blindSpots = append(blindSpots, l)
		}
	}

	return AuditReport{
		RunID:         runID,
		Commit:        commit,
		PlanFull:      plan.Full,
		Tier:          tier,
		SelectedLanes: append([]string(nil), selected...),
		FailingLanes:  sortedFail,
		CoveredLanes:  covered,
		BlindSpots:    blindSpots,
		UnknownLanes:  unknown,
		OK:            len(blindSpots) == 0,
	}
}
