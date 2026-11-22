// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func sampleFindings(t *testing.T) []finding {
	t.Helper()
	t.Setenv("GOWORK", "off")
	sampleHot = true
	t.Cleanup(func() { sampleHot = false })
	root, err := filepath.Abs(filepath.Join("testdata", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := load(root, []string{"./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return scan(root, pkgs)
}

// Each pattern is found once per site in the sample; the package-level regexp, the //perf:ok sites, the functions not marked hot, a MarshalJSON that marshals its own receiver, json.Marshal into json.Unmarshal (json-roundtrip's, not decode-own-encode's), a capped pool and a pool of fixed-size values are not.
func TestScanFindsEachPattern(t *testing.T) {
	var got []string
	for _, f := range sampleFindings(t) {
		for range f.Count {
			got = append(got, f.Pattern+"|"+f.Func+"|"+f.Detail)
		}
	}
	want := []string{
		"compile-in-func|Compile|regexp.MustCompile",
		"env-stat|Env|os.Getenv",
		"sort-instance|Sorts|slices.SortFunc",
		"sort-instance|Sorts|slices.SortStableFunc",
		"sort-instance|Sorts|slices.SortedFunc",
		"sort-instance|Sorts|slices.SortedStableFunc",
		"env-stat|Env|os.Stat",
		"history-clone|History|append(nil,x...)",
		"history-clone|History|slices.Clone",
		"json-roundtrip|RoundTrip|marshal+unmarshal",
		"loop-format|Loop|fmt.Sprintf",
		"loop-format|Loop|string+=",
		"marshal-compare|Compare|bytes.Equal",
		"marshal-compare|Fallback|DeepEqual then Marshal",
		"chan-hop|Hop|send",
		"chan-hop|Hop|<-",
		"chan-hop|Hop|make(chan)",
		"chan-hop|Hop|go",
		"chan-hop|Hop|context.AfterFunc",
		"chan-hop|Hop|time.AfterFunc",
		"chan-hop|Hop|range chan",
		"open-per-call|Open|os.Open",
		"open-per-call|Open|os.ReadFile",
		"timer-per-call|Timer|time.NewTimer",
		"timer-per-call|Timer|time.After",
		"timer-per-call|Retry|time.After",
		"decode-own-encode|Own|sample.MarshalJSON->sample.DecodeEntry",
		"decode-own-encode|OwnStream|json.Marshal->json.NewDecoder",
		"nested-marshal|Wrapper.MarshalJSON|json.Marshal",
		"nested-marshal|Wrapper.MarshalJSON|sample.MarshalJSON",
		"pool-uncapped|PutUncapped|sync.Pool.Put",
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("findings\n got %q\nwant %q", got, want)
	}
}

// A slices sort counts only in a package that cmd/pig links.
func TestSortInstanceCountsOnlyLinkedPackages(t *testing.T) {
	t.Setenv("GOWORK", "off")
	root, err := filepath.Abs(filepath.Join("testdata", "linked"))
	if err != nil {
		t.Fatal(err)
	}
	found, err := findAll(root, []string{"./..."})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range found {
		got = append(got, f.File+"|"+f.Pattern+"|"+f.Detail)
	}
	// a/a.go: two functions, a sort in place and a sorted collection; a/a_vars.go: package-level var initializers (a func literal run at init, a plain call and a sorted collection); a/a_windows.go: linked on windows only.
	want := []string{
		"a/a.go|sort-instance|slices.SortedFunc",
		"a/a.go|sort-instance|slices.SortFunc",
		"a/a_vars.go|sort-instance|slices.SortFunc",
		"a/a_vars.go|sort-instance|slices.SortedStableFunc",
		"a/a_vars.go|sort-instance|slices.SortStableFunc",
		"a/a_windows.go|sort-instance|slices.SortFunc",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("findings\n got %q\nwant %q", got, want)
	}
}

// Baseline entries of the classed patterns must say how hot the site is.
func TestNeedsClassCoversTheNewPatterns(t *testing.T) {
	for _, pattern := range []string{"json-roundtrip", "history-clone", "chan-hop", "decode-own-encode", "nested-marshal", "open-per-call", "pool-uncapped", "timer-per-call", "sort-instance"} {
		if !needsClass(pattern) {
			t.Errorf("%s entries need a class", pattern)
		}
	}
	for _, pattern := range []string{"marshal-compare", "compile-in-func", "env-stat", "loop-format"} {
		if needsClass(pattern) {
			t.Errorf("%s entries were never classed", pattern)
		}
	}
}

func TestCompareRatchetsInBothDirections(t *testing.T) {
	found := sampleFindings(t)
	if grew, stale := compare(found, found); len(grew)+len(stale) != 0 {
		t.Fatalf("exact baseline reported %v %v", grew, stale)
	}
	if grew, _ := compare(found, found[1:]); len(grew) != 1 {
		t.Fatalf("a site missing from the baseline must fail, got %v", grew)
	}
	extra := append(slices.Clone(found), finding{File: "gone.go", Func: "F", Pattern: "x", Count: 1})
	if _, stale := compare(found, extra); len(stale) != 1 {
		t.Fatalf("a baseline entry with no site must fail, got %v", stale)
	}
}

// A baseline that does not parse stops the gate and the -print regeneration alike, with the file and the decode error in the message. -print reads the classes of the old entries from it: read as empty, it would write every entry back without its class.
func TestRunFailsOnABaselineThatDoesNotParse(t *testing.T) {
	t.Setenv("GOWORK", "off")
	root, err := filepath.Abs(filepath.Join("testdata", "sample"))
	if err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(t.TempDir(), "baseline.toml")
	if err := os.WriteFile(baseline, []byte("[[hit]]\nfile = \"a.go\"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, printBaseline := range []bool{false, true} {
		var rc int
		stderr := captureStderr(t, func() { rc = run(root, baseline, []string{"./..."}, false, printBaseline) })
		if rc != 2 || !strings.Contains(stderr, baseline) || !strings.Contains(stderr, "line 2") {
			t.Fatalf("print %v: exit = %d, stderr = %q; want 2 and a message that names %s and line 2", printBaseline, rc, stderr, baseline)
		}
	}
}

// (time.Time).After is a timestamp comparison, not a timer allocation; only package-level time.After counts for timer-per-call.
func TestTimerPerCallIgnoresTimeAfterMethod(t *testing.T) {
	t.Setenv("GOWORK", "off")
	sampleHot = true
	t.Cleanup(func() { sampleHot = false })

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module methodafter\n\ngo 1.24\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := `package methodafter

import "time"

func CompareInLoop(times []time.Time, deadline time.Time) int {
	n := 0
	for _, ts := range times {
		if ts.After(deadline) {
			n++
		}
	}
	return n
}

//perf:hot
func CompareHot(ts, deadline time.Time) bool {
	return ts.After(deadline)
}

func TimerInLoop(n int, d time.Duration) {
	for range n {
		<-time.After(d)
	}
}
`
	if err := os.WriteFile(filepath.Join(dir, "after.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	pkgs, err := load(dir, []string{"./..."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range scan(dir, pkgs) {
		for range f.Count {
			got = append(got, f.Pattern+"|"+f.Func+"|"+f.Detail)
		}
	}
	want := []string{"timer-per-call|TimerInLoop|time.After"}
	if !slices.Equal(got, want) {
		t.Fatalf("findings\n got %q\nwant %q", got, want)
	}
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = file
	defer func() { os.Stderr = old }()
	fn()
	os.Stderr = old
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
