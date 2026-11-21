// SPDX-License-Identifier: MIT

package ciimages

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// conformanceShards reads the shard count SHARDED_SPEC of test-grouped.sh declares for ./test/extension-conformance.
func conformanceShards(t *testing.T) (int, string) {
	t.Helper()
	const pkg = "./test/extension-conformance"
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "automation", "ci", "test-grouped.sh"))
	if err != nil {
		t.Fatal(err)
	}
	spec := regexp.MustCompile(`(?m)^SHARDED_SPEC=\(\n((?:  \S+\n)+)\)$`).FindSubmatch(data)
	if spec == nil {
		t.Fatal("test-grouped.sh does not declare SHARDED_SPEC")
	}
	count := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(pkg) + `:([0-9]+)$`).FindSubmatch(spec[1])
	if count == nil {
		t.Fatalf("SHARDED_SPEC of test-grouped.sh does not shard %s", pkg)
	}
	shards, err := strconv.Atoi(string(count[1]))
	if err != nil || shards < 2 {
		t.Fatalf("SHARDED_SPEC runs %s in %s shards: want two or more", pkg, count[1])
	}
	return shards, pkg
}

// conformanceJobRanges parses the test-conformance-N make targets and returns each matrix shard's inclusive [first, last] range.
func conformanceJobRanges(t *testing.T) (int, map[string][2]int) {
	t.Helper()
	total, _ := conformanceShards(t)
	sources := readMakeSources(t, repoRoot(t))
	re := regexp.MustCompile(`(?m)^(test-conformance-[0-9]+):[^\n]*\n\t@\./automation/ci/test-grouped\.sh conformance ([0-9]+) ([0-9]+)$`)
	matches := re.FindAllStringSubmatch(sources, -1)
	if len(matches) == 0 {
		t.Fatal("no test-conformance-N make targets with a shard range found")
	}
	recipes := map[string][2]int{}
	for _, m := range matches {
		first, _ := strconv.Atoi(m[2])
		last, _ := strconv.Atoi(m[3])
		if first < 1 || last < first || last > total {
			t.Fatalf("%s runs conformance shards %d..%d, outside 1..%d", m[1], first, last, total)
		}
		recipes[m[1]] = [2]int{first, last}
	}
	plan := planFor(t, "merge_group", "")
	jobRanges := map[string][2]int{}
	for _, entry := range plan.Matrix["linux"] {
		if !strings.HasPrefix(entry.Shard, "test-conformance-") {
			continue
		}
		rng, ok := recipes[entry.Shard]
		if !ok {
			t.Fatalf("linux matrix shard %s has no test-conformance-N recipe with a shard range", entry.Shard)
		}
		jobRanges[entry.Shard] = rng
	}
	if len(jobRanges) == 0 {
		t.Fatal("full linux matrix has no test-conformance-* shards")
	}
	return total, jobRanges
}

// TestLinuxConformanceJobsCoverEveryShardExactlyOnce verifies that the union of (./test/extension-conformance, shard i of 20)
// executed across the Linux test-conformance-* matrix jobs equals 1..20 with no dropped or duplicate shards.
func TestLinuxConformanceJobsCoverEveryShardExactlyOnce(t *testing.T) {
	total, jobRanges := conformanceJobRanges(t)
	runs := make([]int, total+1)
	for name, rng := range jobRanges {
		for shard := rng[0]; shard <= rng[1]; shard++ {
			if shard < 1 || shard > total {
				t.Fatalf("%s runs conformance shard %d of %d", name, shard, total)
			}
			runs[shard]++
		}
	}
	for shard := 1; shard <= total; shard++ {
		if runs[shard] != 1 {
			t.Errorf("./test/extension-conformance shard %d/%d runs in %d Linux conformance jobs, want exactly 1", shard, total, runs[shard])
		}
	}
}

// Per-shard go test durations (seconds) measured on GitHub's 4-CPU ubuntu-24.04 runners:
// - warm: run 44864086404 (2026-11-21, jobs 136727289896 and 136727289921)
// - cold: run 44880357753 (2026-11-21, job 136777514733 for shards 1..10; shards 11..20 scaled by the 1.371x cold/warm ratio observed on shards 1..10 so every job is bounded for a cold runner)
// Setup and inter-shard overhead from the same runs:
// - warm: 636 s setup (checkout, caches, make test-warm, prereqs) + 10 s per shard
// - cold: 1020 s setup (17 min worst observed cold setup across runs 44870314130 and 44880357753) + 11 s per shard
var (
	linuxConformanceWarmShardSeconds = [20]int{
		177, 303, 201, 251, 173, 197, 143, 224, 153, 174,
		222, 225, 188, 261, 184, 334, 197, 208, 179, 179,
	}
	linuxConformanceColdShardSeconds = [20]int{
		241, 459, 254, 341, 230, 267, 192, 310, 209, 233,
		305, 308, 257, 358, 253, 458, 270, 285, 245, 246,
	}
)

const (
	linuxConformanceWarmSetupSeconds    = 636
	linuxConformanceWarmPerShardSeconds = 10
	linuxConformanceColdSetupSeconds    = 1020
	linuxConformanceColdPerShardSeconds = 11
	linuxConformanceColdMarginSeconds   = 600 // at least 10 minutes under the 60-minute limit even on a cold runner
)

// TestLinuxConformanceJobsFitTheirBudget guards against regressions like runs 44870314130 and 44880357753 where
// Linux / test-conformance-1 was cancelled at timeout-minutes: 60 on cold caches with every shard passing.
// Every Linux conformance job must stay at or under 60% of the job limit warm (2160 s of 3600 s) and leave at least
// 10 minutes of margin cold.
func TestLinuxConformanceJobsFitTheirBudget(t *testing.T) {
	limit := verificationJobs(t)["linux"].Timeout * 60
	if limit <= 0 || limit > 3600 {
		t.Fatalf("linux timeout = %d s, want 1..3600", limit)
	}
	total, jobRanges := conformanceJobRanges(t)
	if total != len(linuxConformanceWarmShardSeconds) {
		t.Fatalf("conformance shard count = %d, timing table has %d entries: update timings", total, len(linuxConformanceWarmShardSeconds))
	}
	for name, rng := range jobRanges {
		warm := linuxConformanceWarmSetupSeconds
		cold := linuxConformanceColdSetupSeconds
		for shard := rng[0]; shard <= rng[1]; shard++ {
			warm += linuxConformanceWarmShardSeconds[shard-1] + linuxConformanceWarmPerShardSeconds
			cold += linuxConformanceColdShardSeconds[shard-1] + linuxConformanceColdPerShardSeconds
		}
		if warm*5 > limit*3 {
			t.Errorf("linux job %s (shards %d..%d) takes %d s warm, more than 60%% of its %d s limit", name, rng[0], rng[1], warm, limit)
		}
		if cold > limit-linuxConformanceColdMarginSeconds {
			t.Errorf("linux job %s (shards %d..%d) takes %d s cold, leaving less than %d s margin under its %d s limit", name, rng[0], rng[1], cold, linuxConformanceColdMarginSeconds, limit)
		}
	}
}
