// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"slices"
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
	pkgs, err := load(root, []string{"./..."})
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

// Baseline entries of the classed patterns must say how hot the site is.
func TestNeedsClassCoversTheNewPatterns(t *testing.T) {
	for _, pattern := range []string{"json-roundtrip", "history-clone", "chan-hop", "decode-own-encode", "nested-marshal", "open-per-call", "pool-uncapped", "timer-per-call"} {
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
