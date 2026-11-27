// SPDX-License-Identifier: MIT

package cidag

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// generateRandomFixture builds a random DAG of main-module Go packages across
// linux/windows/darwin with random compile imports, embed files, testdata,
// and GOOS-only tests to stress-test all five formal soundness theorems.
func generateRandomFixture(rng *rand.Rand, numPkgs int) (*Graph, []string) {
	mod := DefaultModule
	modInfo := &ModInfo{Path: mod}
	pkgNames := make([]string, numPkgs)
	for i := range numPkgs {
		pkgNames[i] = fmt.Sprintf("pkg/p%02d", i)
	}
	// Always include the three global guard packages so Theorem 4 checks both
	// global gates and global guard packages.
	fixed := []string{"internal/testenv", "test/ci-images", "test/docs-drift", "coding/cli", "cmd/pig"}
	allDirs := make([]string, 0, len(pkgNames)+len(fixed))
	allDirs = append(allDirs, pkgNames...)
	allDirs = append(allDirs, fixed...)

	// Build an acyclic dependency graph (higher index may import lower index).
	importsByIdx := make([][]string, len(allDirs))
	for i := 1; i < len(allDirs); i++ {
		for j := range i {
			if rng.IntN(4) == 0 {
				importsByIdx[i] = append(importsByIdx[i], mod+"/"+allDirs[j])
			}
		}
	}

	makePkgs := func(goos string) []ListPackage {
		pkgs := make([]ListPackage, 0, len(allDirs))
		for i, dir := range allDirs {
			lp := ListPackage{
				ImportPath:  mod + "/" + dir,
				Dir:         "/sim/" + dir,
				Module:      modInfo,
				GoFiles:     []string{"src.go"},
				TestGoFiles: []string{"src_test.go"},
				Imports:     importsByIdx[i],
				Deps:        importsByIdx[i],
			}
			if i < numPkgs && i%3 == 0 {
				lp.EmbedFiles = []string{"asset.txt"}
			}
			if i < numPkgs && i%4 == 0 {
				lp.TestEmbedFiles = []string{"tfix.json"}
			}
			if goos == "windows" && i%5 == 0 {
				lp.TestGoFiles = append(lp.TestGoFiles, "src_windows_test.go")
			}
			if goos == "darwin" && i%6 == 0 {
				lp.TestGoFiles = append(lp.TestGoFiles, "src_darwin_test.go")
			}
			pkgs = append(pkgs, lp)
		}
		return pkgs
	}

	g, err := BuildGraph(GraphInput{
		Root:   "/sim",
		Module: mod,
		PackagesByGOOS: map[string][]ListPackage{
			"linux":   makePkgs("linux"),
			"windows": makePkgs("windows"),
			"darwin":  makePkgs("darwin"),
		},
		ReadFile: func(string) ([]byte, error) { return []byte("package p\n"), nil },
	})
	if err != nil {
		panic(err)
	}

	// Build a diverse universe of files (docs, rules, compile .go, test .go,
	// embed, testdata, unowned).
	universe := []string{
		"README.md",
		"CHANGELOG.md",
		"changelog.d/note.md",
		"docs/guide.md",
		"extensions/sdk-rs/src/lib.rs",
		"extensions/sdk-py/pig_sdk/client.py",
		"extensions/sdk-ts/index.ts",
		"coding/extension/host/subprocess/protocol.go",
		"Makefile",
		"go.mod",
		"unknown/file_not_owned.xyz",
	}
	for i := range numPkgs {
		dir := pkgNames[i]
		universe = append(universe, dir+"/src.go", dir+"/src_test.go", dir+"/testdata/f.json")
		if i%3 == 0 {
			universe = append(universe, dir+"/asset.txt")
		}
		if i%4 == 0 {
			universe = append(universe, dir+"/tfix.json")
		}
	}
	return g, universe
}

func isSubset(sub, sup []string) bool {
	supSet := make(map[string]bool, len(sup))
	for _, x := range sup {
		supSet[x] = true
	}
	for _, x := range sub {
		if !supSet[x] {
			return false
		}
	}
	return true
}

func randomSubset(rng *rand.Rand, universe []string, maxSize int) []string {
	k := rng.IntN(maxSize + 1)
	seen := map[string]bool{}
	out := make([]string, 0, k)
	for range k {
		f := universe[rng.IntN(len(universe))]
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// TestSoundnessTheoremsPropertyVerifier exhaustively checks all five formal
// theorems from automation/ci/cidag/proof/SelectionSoundness.lean over 250
// randomized package graphs and change sets.
func TestSoundnessTheoremsPropertyVerifier(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261124, 42))
	noRead := func(string) ([]byte, error) { return []byte("package p\n"), nil }

	for trial := range 50 {
		numPkgs := 6 + rng.IntN(10)
		g, universe := generateRandomFixture(rng, numPkgs)

		// Theorem 1: Monotonicity: S1 ⊆ S2 => plan(S1).Lanes ⊆ plan(S2).Lanes
		//            and plan(S1).PRLanes ⊆ plan(S2).PRLanes
		//            and (when !plan(S2).Full) plan(S1).AffectedPackages ⊆ plan(S2).AffectedPackages.
		for range 5 {
			s1 := randomSubset(rng, universe, 6)
			extra := randomSubset(rng, universe, 6)
			s2 := append(append([]string(nil), s1...), extra...)

			p1 := Select(s1, g, DefaultRules, Options{ReadFile: noRead})
			p2 := Select(s2, g, DefaultRules, Options{ReadFile: noRead})

			if p1.Full && !p2.Full {
				t.Fatalf("trial %d Monotonicity violated: S1=%v gave Full=true but S2=%v gave Full=false", trial, s1, s2)
			}
			if !isSubset(p1.Lanes, p2.Lanes) {
				t.Fatalf("trial %d Monotonicity violated on Lanes: S1=%v -> %v, S2=%v -> %v", trial, s1, p1.Lanes, s2, p2.Lanes)
			}
			if !isSubset(p1.PRLanes, p2.PRLanes) {
				t.Fatalf("trial %d Monotonicity violated on PRLanes: S1=%v -> %v, S2=%v -> %v", trial, s1, p1.PRLanes, s2, p2.PRLanes)
			}
			if !p2.Full && !isSubset(p1.AffectedPackages, p2.AffectedPackages) {
				t.Fatalf("trial %d Monotonicity violated on AffectedPackages: S1=%v -> %v, S2=%v -> %v", trial, s1, p1.AffectedPackages, s2, p2.AffectedPackages)
			}
		}

		// Theorem 2: Transitive Closure Soundness: for any compile-edge package p
		// modified in S, every package q in ReverseClosure(p) is in AffectedPackages
		// (or plan.Full is true).
		for i := range numPkgs {
			f := fmt.Sprintf("pkg/p%02d/src.go", i)
			p := Select([]string{f}, g, DefaultRules, Options{ReadFile: noRead})
			if p.Full {
				continue
			}
			modPkg := g.Module + fmt.Sprintf("/pkg/p%02d", i)
			closure := g.ReverseClosure(map[string]bool{modPkg: true})
			for dep := range closure {
				sp := g.ShortPkg(dep)
				if !slices.Contains(p.AffectedPackages, sp) {
					t.Fatalf("trial %d Transitive Closure Soundness violated: editing %s missed reverse dep %s in %v",
						trial, f, sp, p.AffectedPackages)
				}
			}
		}

		// Theorem 3: Fail-Closed Completeness:
		// (a) any unowned file in S => plan(S).Full == true && plan(S).Lanes == AllLanes()
		// (b) |S| > maxFiles => plan(S).Full == true && plan(S).Lanes == AllLanes().
		sWithUnowned := append(randomSubset(rng, universe, 4), "totally/unknown/file.bin")
		pUnowned := Select(sWithUnowned, g, DefaultRules, Options{ReadFile: noRead})
		if !pUnowned.Full || !slices.Equal(pUnowned.Lanes, AllLanes()) {
			t.Fatalf("trial %d Fail-Closed Completeness (unowned) violated: full=%v lanes=%d/%d",
				trial, pUnowned.Full, len(pUnowned.Lanes), len(AllLanes()))
		}
		pTooMany := Select([]string{"README.md", "CHANGELOG.md", "docs/guide.md"}, g, DefaultRules, Options{MaxFiles: 2, ReadFile: noRead})
		if !pTooMany.Full || !slices.Equal(pTooMany.Lanes, AllLanes()) {
			t.Fatalf("trial %d Fail-Closed Completeness (maxFiles) violated: full=%v", trial, pTooMany.Full)
		}

		// Theorem 4: Global-Invariant Lock-In:
		// For every non-docs-only change S, GlobalGateLanes ⊆ plan(S).Lanes,
		// GlobalGateLanes ⊆ plan(S).PRLanes, and GlobalGuardPackages ⊆ AffectedPackages (or Full).
		for range 5 {
			s := randomSubset(rng, universe, 6)
			p := Select(s, g, DefaultRules, Options{ReadFile: noRead})
			if !p.DocsOnly {
				if !isSubset(GlobalGateLanes, p.Lanes) {
					t.Fatalf("trial %d Global-Invariant Lock-In violated on Lanes for %v: missing gate in %v", trial, s, p.Lanes)
				}
				if !isSubset(GlobalGateLanes, p.PRLanes) {
					t.Fatalf("trial %d Global-Invariant Lock-In violated on PRLanes for %v: missing gate in %v", trial, s, p.PRLanes)
				}
				if !p.Full && !isSubset(GlobalGuardPackages, p.AffectedPackages) {
					t.Fatalf("trial %d Global-Invariant Lock-In violated on GuardPackages for %v: %v", trial, s, p.AffectedPackages)
				}
			}
		}

		// Theorem 5: Tiering Preservation:
		// (a) plan(S).PRLanes ⊆ plan(S).Lanes
		// (b) In label mode, RenderCIPlan on pull_request without fullRequested yields
		//     Full=false and FullRequired=true, so ciResultPasses(light) == false.
		for range 5 {
			s := randomSubset(rng, universe, 6)
			p := Select(s, g, DefaultRules, Options{ReadFile: noRead})
			if !isSubset(p.PRLanes, p.Lanes) {
				t.Fatalf("trial %d Tiering Preservation violated: PRLanes=%v not subset of Lanes=%v", trial, p.PRLanes, p.Lanes)
			}
		}
	}

	// Theorem 5(b): verify ciResultPasses(light) == false in label mode across RenderCIPlan.
	rows := []TierRow{
		{Job: "linux", Shard: "build", Tier: TierPR, LightTarget: "ci-build-pr"},
		{Job: "linux", Shard: "checks", Tier: TierPR, LightTarget: "ci-checks-pr"},
		{Job: "linux", Shard: "contracts", Tier: TierPR},
		{Job: "linux", Shard: "portmap-1", Tier: TierQueue},
		{Job: "windows", Shard: "native-1", Tier: TierQueue},
		{Job: "macos", Shard: "native", Tier: TierQueue},
		{Job: "single", Shard: "node-runtime", Tier: TierQueue},
	}
	lightOut, err := RenderCIPlan(rows, "pull_request", "label", false, nil)
	if err != nil {
		t.Fatalf("RenderCIPlan label light: %v", err)
	}
	if lightOut.Full || !lightOut.FullRequired {
		t.Fatalf("Theorem 5(b) violated: label mode light run got Full=%v FullRequired=%v, want false/true",
			lightOut.Full, lightOut.FullRequired)
	}
	// Aggregator contract (matches ci.yml): if fullRequired && !full => CI result fails.
	ciResultPasses := func(o CIPlanOutput, product bool) bool {
		if !product {
			return true
		}
		return !o.FullRequired || o.Full
	}
	if ciResultPasses(lightOut, true) {
		t.Fatalf("Theorem 5(b) violated: light run passed CI result in label mode")
	}
	fullOut, err := RenderCIPlan(rows, "pull_request", "label", true, nil)
	if err != nil {
		t.Fatalf("RenderCIPlan label full: %v", err)
	}
	if !ciResultPasses(fullOut, true) {
		t.Fatalf("Theorem 5(b) violated: full run failed CI result in label mode")
	}
}
