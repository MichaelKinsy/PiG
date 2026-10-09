package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// A function that only a parity-harness probe calls is not production-reachable; the same function stays reachable when a
// production path also calls it.
func TestReachableSkipsParityHarnessProbes(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                 "module example.com/reach\n\ngo 1.26\n",
		"main.go":                "package main\n\nfunc main() { shared(); probe() }\n\nfunc shared() {}\n",
		"parity_harness.go":      "package main\n\nfunc probe() { harnessOnly(); shared() }\n",
		"harness_only_target.go": "package main\n\nfunc harnessOnly() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := reachable(dir, []string{"."}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main.go#main", "main.go#shared"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	for _, absent := range []string{"parity_harness.go#probe", "harness_only_target.go#harnessOnly"} {
		if slices.Contains(got, absent) {
			t.Errorf("%s reachable only through the harness is listed: %v", absent, got)
		}
	}
}

// A binary that a build tag selects (cmd/pig with pig_experimental is pig-experimental) is reachable only in a build with that tag; reachableUnion lists it next to the default build's functions.
func TestReachableUnionAddsTheTaggedBuild(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":       "module example.com/reach\n\ngo 1.26\n",
		"main_def.go":  "//go:build !tagged\n\npackage main\n\nfunc main() { plain() }\n\nfunc plain() {}\n",
		"main_tag.go":  "//go:build tagged\n\npackage main\n\nfunc main() { onlyTagged() }\n\nfunc onlyTagged() {}\n",
		"unreached.go": "package main\n\nfunc unreached() {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plain, err := reachableUnion(dir, []string{"."}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain, "main_tag.go#onlyTagged") {
		t.Fatalf("the default build lists a tagged function: %v", plain)
	}
	both, err := reachableUnion(dir, []string{"."}, []string{"tagged"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"main_def.go#plain", "main_tag.go#onlyTagged"} {
		if !slices.Contains(both, want) {
			t.Errorf("%s missing from the union %v", want, both)
		}
	}
	if slices.Contains(both, "unreached.go#unreached") {
		t.Errorf("an unreached function is listed: %v", both)
	}
}

// The builds of a union run concurrently; the union is still the sorted merge of each build alone, and a build that fails to compile
// fails the union with its own error instead of being dropped.
func TestReachableUnionConcurrentBuildsMatchSequentialAndSurfaceErrors(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/reach\n\ngo 1.26\n",
		"main_def.go": "//go:build !a && !b && !broken\n\npackage main\n\nfunc main() { plain() }\n\nfunc plain() {}\n",
		"main_a.go":   "//go:build a\n\npackage main\n\nfunc main() { onlyA() }\n\nfunc onlyA() {}\n",
		"main_b.go":   "//go:build b\n\npackage main\n\nfunc main() { onlyB() }\n\nfunc onlyB() {}\n",
		"main_bad.go": "//go:build broken\n\npackage main\n\nfunc main() { undefinedCall() }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var want []string
	for _, tags := range []string{"", "a", "b"} {
		one, err := reachable(dir, []string{"."}, tags)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, one...)
	}
	slices.Sort(want)
	want = slices.Compact(want)
	got, err := reachableUnion(dir, []string{"."}, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("concurrent union %v differs from the merge of the builds %v", got, want)
	}
	for _, entry := range []string{"main_def.go#plain", "main_a.go#onlyA", "main_b.go#onlyB"} {
		if !slices.Contains(got, entry) {
			t.Errorf("%s missing from %v", entry, got)
		}
	}
	if _, err := reachableUnion(dir, []string{"."}, []string{"a", "broken"}); err == nil {
		t.Fatal("a build that does not compile did not fail the union")
	}
}
