// SPDX-License-Identifier: MIT

package main

import "testing"

// make alloc-baseline stops with rc 2 when baseline.toml does not load, and the unit test of loadBaseline on a scratch file never reads the committed one: this test loads the committed file, so a malformed or repeated entry fails in the package's own tests.
func TestCommittedBaselineLoadsAndNamesEveryBenchmarkOnce(t *testing.T) {
	baseline, err := loadBaseline("baseline.toml")
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline.Benchmark) == 0 {
		t.Fatal("the committed baseline.toml has no entries")
	}
	seen := map[string]int{}
	for i, e := range baseline.Benchmark {
		if e.Package == "" || e.Benchmark == "" {
			t.Errorf("entry %d has an empty package or benchmark: %+v", i+1, e)
		}
		if first, ok := seen[e.key()]; ok {
			t.Errorf("entry %d repeats entry %d: %s", i+1, first, e.key())
		}
		seen[e.key()] = i + 1
	}
	t.Logf("baseline.toml loads: %d entries, each named once", len(baseline.Benchmark))
}
