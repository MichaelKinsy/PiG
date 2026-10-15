// SPDX-License-Identifier: MIT

package main

import (
	"slices"
	"testing"
)

// The run sets GOMAXPROCS=1, so a trailing -<digits> is part of a sub-benchmark's name: edits-1 and edits-1000 are two benchmarks, not one.
func TestParseResultsReadsAllocsPerOpWithExtraMetrics(t *testing.T) {
	output := []byte(`goos: linux
BenchmarkPlain                     	       5	      1234 ns/op	     512 B/op	      17 allocs/op
BenchmarkSub/case=1                	       5	      1234 ns/op	  64.0 frames/op	     512 B/op	    1700 allocs/op
BenchmarkThroughput/clean         	       5	     33410 ns/op	1508.54 MB/s	       0 B/op	       0 allocs/op
BenchmarkSub/edits-1               	       5	      1234 ns/op	     512 B/op	      21 allocs/op
BenchmarkSub/edits-1000            	       5	      1234 ns/op	     512 B/op	    4000 allocs/op
BenchmarkNoMem                     	       5	      1234 ns/op
PASS
`)
	want := []entry{{"./ai", "BenchmarkPlain", 17}, {"./ai", "BenchmarkSub/case=1", 1700}, {"./ai", "BenchmarkThroughput/clean", 0}, {"./ai", "BenchmarkSub/edits-1", 21}, {"./ai", "BenchmarkSub/edits-1000", 4000}}
	if got := parseResults("./ai", output); !slices.Equal(got, want) {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
}

// A raise and a lowering beyond the tolerance, a benchmark with no entry and an entry with no benchmark all fail; a change within the tolerance does not.
func TestCompareRatchetsBothWays(t *testing.T) {
	baseline := []entry{{"p", "A", 100}, {"p", "B", 100}, {"p", "C", 100}, {"p", "D", 100}, {"p", "Gone", 5}, {"p", "Tiny", 2}}
	found := []entry{{"p", "A", 100 + 6}, {"p", "B", 100 + 7}, {"p", "C", 100 - 7}, {"p", "D", 100}, {"p", "New", 9}, {"p", "Tiny", 4}}
	problems := compare(found, baseline)
	if len(problems) != 4 {
		t.Fatalf("problems = %q, want B raised, C lowered, New, Gone", problems)
	}
}

// Two results or two baseline entries under one key would compare one benchmark with another's count.
func TestCompareRejectsRepeatedKeys(t *testing.T) {
	baseline := []entry{{"p", "A", 100}, {"p", "A", 200}, {"p", "B", 10}}
	found := []entry{{"p", "A", 200}, {"p", "B", 10}, {"p", "B", 10}}
	problems := compare(found, baseline)
	if len(problems) != 2 {
		t.Fatalf("problems = %q, want the repeated baseline key A and the repeated result B", problems)
	}
}
