// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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

// A sub-benchmark that reports its own allocs/op with b.ReportMetric (a per-unit figure, as BenchmarkReload_PackedCellWarmCache's rows are) is read as that
// figure: go test prints one allocs/op for it, the reported one, and not a second beside the count of the testing package.
func TestParseResultsReadsAReportedAllocsPerOp(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module reported\n\ngo 1.26\n",
		"reported_test.go": `package reported

import "testing"

func BenchmarkReported(b *testing.B) {
	b.Run("per-100-things", func(b *testing.B) {
		b.ReportAllocs()
		sink := make([][]byte, 0, b.N)
		for i := 0; i < b.N; i++ {
			sink = append(sink, make([]byte, 64))
		}
		_ = sink
		b.ReportMetric(1234, "allocs/op")
	})
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-run", "^$", "-bench", ".", "-benchmem", "-benchtime", "5x")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOMAXPROCS=1", "GOFLAGS=-mod=mod")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("go test: %v\n%s", err, output)
	}
	if strings.Count(string(output), "allocs/op") != 1 {
		t.Fatalf("want one allocs/op in the output, got:\n%s", output)
	}
	want := []entry{{"./reported", "BenchmarkReported/per-100-things", 1234}}
	if got := parseResults("./reported", output); !slices.Equal(got, want) {
		t.Fatalf("parsed %+v from\n%s\nwant %+v", got, output, want)
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

// A baseline that does not parse is an error that names the file and the line; a missing one is an empty baseline.
func TestLoadBaselineNamesTheFileThatDoesNotParse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.toml")
	if baseline, err := loadBaseline(path); err != nil || len(baseline.Benchmark) != 0 {
		t.Fatalf("missing baseline = %+v, %v; want empty and no error", baseline, err)
	}
	if err := os.WriteFile(path, []byte("[[benchmark]]\npackage = \"./ai\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadBaseline(path); err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want it to name %s and line 2", err, path)
	}
}
