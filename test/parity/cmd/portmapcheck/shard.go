package main

import (
	"fmt"
	"hash/fnv"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// maxShards bounds the shard count a -shard flag may name; CI plans three.
const maxShards = 64

// shardSpec selects one of total shards; total 0 means every row. index is zero-based.
type shardSpec struct{ index, total int }

// parseShard reads `N/M` (1 <= N <= M <= maxShards). The empty string is every row. A value that is not a decimal integer, overflows an int
// or lies outside those bounds is an error, so the shard count that reaches partition is always in range.
func parseShard(s string) (shardSpec, error) {
	if s == "" {
		return shardSpec{}, nil
	}
	n, m, ok := strings.Cut(s, "/")
	// ParseUint takes digits only (no sign, no space) and, at 8 bits, refuses anything past 255, so both numbers are small before they are used.
	i, err1 := strconv.ParseUint(n, 10, 8)
	total, err2 := strconv.ParseUint(m, 10, 8)
	if !ok || err1 != nil || err2 != nil || i < 1 || i > total || total > maxShards {
		return shardSpec{}, fmt.Errorf("-shard %q: want N/M with 1 <= N <= M <= %d", s, maxShards)
	}
	return shardSpec{index: int(i) - 1, total: int(total)}, nil
}

// buildWeight and mutantWeight price what a row costs besides running its tests: one test binary per evidence package and one compile
// of the mutated package per marker row. A test counts 1. Measured on a 4-CPU runner, a test binary takes about 9 s, a mutant replay
// about 8 s and a test about 0.2 s, so both cost about 40 tests.
const (
	buildWeight  = 45
	mutantWeight = 40
)

// rowWeight is the planning cost of a row's evidence run. It only balances the shards; no verdict depends on it.
func rowWeight(r result) int {
	w := len(r.Tests)
	if r.Marker {
		w += mutantWeight
	}
	return w
}

// primaryDir is the evidence package that holds most of a row's tests, the lexically first on a tie.
func primaryDir(r result) string {
	count := map[string]int{}
	for _, t := range r.Tests {
		dir, _, _ := strings.Cut(t, "#")
		count[dir]++
	}
	best := ""
	for dir, n := range count {
		if best == "" || n > count[best] || n == count[best] && dir < best {
			best = dir
		}
	}
	return best
}

// partition assigns each row to one of total shards and returns the shard of every row, by index. It is a pure function of the derived
// rows, so every shard computes the same plan. Rows that run evidence tests stay together by primary evidence package, so a shard builds
// that package's test binary once; a package heavier than a shard's fair share is split across shards by row. Greedy longest-first
// placement keeps the loads close. Rows with no evidence run (already unproven by the derivation) cost nothing and are placed by a hash
// of their Pi path so that exactly one shard reports each.
func partition(res []result, total int) []int {
	if total < 1 || total > maxShards {
		panic(fmt.Sprintf("partition: %d shards, want 1 to %d", total, maxShards)) // parseShard has already refused such a count
	}
	out := make([]int, len(res))
	type group struct {
		dir    string
		rows   []int
		weight int
	}
	byDir := map[string]*group{}
	sum := 0
	for i, r := range res {
		if !r.Proven || len(r.Tests) == 0 {
			h := fnv.New32a()
			_, _ = h.Write([]byte(r.Row.Pi))
			out[i] = int(h.Sum32() % uint32(total))
			continue
		}
		dir := primaryDir(r)
		g := byDir[dir]
		if g == nil {
			g = &group{dir: dir, weight: buildWeight}
			byDir[dir] = g
			sum += buildWeight
		}
		g.rows = append(g.rows, i)
		g.weight += rowWeight(r)
		sum += rowWeight(r)
	}
	groups := make([]*group, 0, len(byDir))
	for _, g := range byDir {
		groups = append(groups, g)
	}
	sort.Slice(groups, func(a, b int) bool {
		if groups[a].weight != groups[b].weight {
			return groups[a].weight > groups[b].weight
		}
		return groups[a].dir < groups[b].dir
	})
	load := make([]int, total)
	lightest := func() int {
		best := 0
		for s := 1; s < total; s++ {
			if load[s] < load[best] {
				best = s
			}
		}
		return best
	}
	share := (sum + total - 1) / total
	for _, g := range groups {
		if g.weight <= share {
			s := lightest()
			for _, i := range g.rows {
				out[i] = s
			}
			load[s] += g.weight
			continue
		}
		// Heavier than a shard's share: spread its rows, heaviest first, each shard that takes one pays for its own build.
		rows := slices.Clone(g.rows)
		sort.SliceStable(rows, func(a, b int) bool { return rowWeight(res[rows[a]]) > rowWeight(res[rows[b]]) })
		built := make([]bool, total)
		for _, i := range rows {
			s := lightest()
			if !built[s] {
				built[s] = true
				load[s] += buildWeight
			}
			out[i] = s
			load[s] += rowWeight(res[i])
		}
	}
	return out
}
