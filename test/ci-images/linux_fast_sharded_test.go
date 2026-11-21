// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// fastShardedPackages returns every package in SHARDED_SPEC of test-grouped.sh except the extension host and conformance
// suites (which run in test-subprocess and test-conformance-*).
func fastShardedPackages(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "test-grouped.sh"))
	if err != nil {
		t.Fatal(err)
	}
	spec := regexp.MustCompile(`(?m)^SHARDED_SPEC=\(\n((?:  \S+\n)+)\)$`).FindSubmatch(data)
	if spec == nil {
		t.Fatal("test-grouped.sh does not declare SHARDED_SPEC")
	}
	var pkgs []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(spec[1])), "\n") {
		pkg, _, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if pkg == "./coding/extension/host/subprocess" || pkg == "./test/extension-conformance" {
			continue
		}
		pkgs = append(pkgs, pkg)
	}
	if len(pkgs) == 0 {
		t.Fatal("no fast-sharded packages found in SHARDED_SPEC")
	}
	return pkgs
}

func fastShardedHalfOf(t *testing.T, pkg string) int {
	t.Helper()
	switch pkg {
	case "./ai", "./tui", "./tui/internal/hljs":
		return 1
	default:
		return 2
	}
}

// TestLinuxFastShardedHalvesCoverEveryPackageOnce verifies that the two Linux test-fast-sharded jobs cover the exact
// union of fast-sharded packages with no dropped or duplicated package, and that make test-fast-sharded still runs all.
func TestLinuxFastShardedHalvesCoverEveryPackageOnce(t *testing.T) {
	sources := readMakeSources(t, repoRoot(t))
	if !strings.Contains(sources, "test-fast-sharded: test-prereqs interface-deps parity-deps durable-interop-deps\n\t@./automation/ci/test-grouped.sh fast-sharded\n") {
		t.Fatal("make test-fast-sharded must remain the full union of fast-sharded packages")
	}
	re := regexp.MustCompile(`(?m)^(test-fast-sharded-[0-9]+):[^\n]*\n\t@\./automation/ci/test-grouped\.sh fast-sharded ([0-9]+) ([0-9]+)$`)
	matches := re.FindAllStringSubmatch(sources, -1)
	if len(matches) != 2 {
		t.Fatalf("found %d test-fast-sharded-N targets, want 2", len(matches))
	}
	seenParts := map[int]bool{}
	for _, m := range matches {
		part, _ := strconv.Atoi(m[2])
		total, _ := strconv.Atoi(m[3])
		if total != 2 || part < 1 || part > 2 {
			t.Fatalf("%s runs slice %d/%d, want 1/2 or 2/2", m[1], part, total)
		}
		seenParts[part] = true
	}
	if !seenParts[1] || !seenParts[2] {
		t.Fatalf("test-fast-sharded halves cover parts %v, want 1 and 2", seenParts)
	}
	plan := planFor(t, "merge_group", "")
	var shards []string
	for _, entry := range plan.Matrix["linux"] {
		if entry.Shard == "test-fast-sharded" || strings.HasPrefix(entry.Shard, "test-fast-sharded-") {
			shards = append(shards, entry.Shard)
		}
	}
	slices.Sort(shards)
	wantShards := []string{"test-fast-sharded", "test-fast-sharded-2"}
	if !slices.Equal(shards, wantShards) {
		t.Fatalf("linux matrix fast-sharded jobs = %v, want %v", shards, wantShards)
	}
	// Verify every fast-sharded package lands in exactly one half.
	pkgs := fastShardedPackages(t)
	counts := map[string]int{}
	for _, pkg := range pkgs {
		h := fastShardedHalfOf(t, pkg)
		if h != 1 && h != 2 {
			t.Fatalf("package %s assigned to invalid half %d", pkg, h)
		}
		counts[pkg]++
	}
	for _, pkg := range pkgs {
		if counts[pkg] != 1 {
			t.Errorf("package %s assigned %d times, want 1", pkg, counts[pkg])
		}
	}
}

// Per-package wall seconds on the fast pool (us-east-1, run 44880357753, job 136777514718).
var linuxFastShardedFastPoolSeconds = map[string]int{
	"./ai":                   501,
	"./tui":                  427,
	"./tui/internal/hljs":    135,
	"./internal/codingagent": 380,
	"./coding":               242,
	"./test/parity/interface-closure/autobind": 187,
	"./codemode":                     77,
	"./test/parity/cmd/portmapcheck": 23,
	"./durable/codemode":             19,
}

// TestLinuxFastShardedHalvesFitTheirBudget pins that each half of Linux / test-fast-sharded stays <= 60% of the
// 60-minute limit on the fast pool and <= 85% on the slow pool (1.4x fast-pool projection).
func TestLinuxFastShardedHalvesFitTheirBudget(t *testing.T) {
	limit := verificationJobs(t)["linux"].Timeout * 60
	if limit <= 0 || limit > 3600 {
		t.Fatalf("linux timeout = %d s, want 1..3600", limit)
	}
	const fastSetupSec = 600 // checkout, caches, warm fixtures (~10 m on us-east-1)
	halfFast := map[int]int{1: fastSetupSec, 2: fastSetupSec}
	for _, pkg := range fastShardedPackages(t) {
		sec, ok := linuxFastShardedFastPoolSeconds[pkg]
		if !ok {
			t.Fatalf("fast-sharded package %s missing from linuxFastShardedFastPoolSeconds timing table", pkg)
		}
		halfFast[fastShardedHalfOf(t, pkg)] += sec
	}
	for half, fastSec := range halfFast {
		slowSec := (fastSec*14 + 9) / 10
		if fastSec*5 > limit*3 {
			t.Errorf("test-fast-sharded half %d projects %d s fast-pool (> 60%% of %d s limit)", half, fastSec, limit)
		}
		if slowSec*100 > limit*85 {
			t.Errorf("test-fast-sharded half %d projects %d s slow-pool (> 85%% of %d s limit)", half, slowSec, limit)
		}
	}
}
