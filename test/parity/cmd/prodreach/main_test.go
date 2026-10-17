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
	got, err := reachable(dir, []string{"."}, nil, "")
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

// A library package's exported API is reachable when the package is named with -library: its exported functions, the exported
// methods of its exported types, the instances non-test code builds of its exported generic API, and what they call. Unexported API,
// generic code nothing instantiates and the package without -library are not.
func TestALibraryPackagesExportedAPIIsReachable(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/reach\n\ngo 1.26\n",
		"cmd/app/main.go": "package main\n\nfunc main() {}\n",
		"lib/lib.go": "package lib\n\ntype Env struct{}\n\nfunc New() *Env { return &Env{} }\n\nfunc (e *Env) Run() { helper() }\n\n" +
			"func (Env) Name() string { return name() }\n\nfunc helper() {}\n\nfunc name() string { return \"\" }\n\n" +
			"type hidden struct{}\n\nfunc (hidden) Visible() {}\n\nfunc unused() {}\n\n" +
			"type Box[T any] struct{ v T }\n\nfunc (b Box[T]) Get() T { return b.v }\n\nfunc Wrap[T any](v T) Box[T] { return Box[T]{v} }\n\n" +
			"func unusedInts() int { return Wrap(1).Get() }\n\nfunc Nested[T any](v T) T { return Wrap(v).Get() }\n\n" +
			"type Pair[T any] struct{ a, b T }\n\nfunc (p Pair[T]) Swap() Pair[T] { return Pair[T]{p.b, p.a} }\n\ntype Runner interface{ Run() }\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plain, err := reachable(dir, []string{"./cmd/app"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain, "lib/lib.go#New") {
		t.Fatalf("without -library the library is reachable: %v", plain)
	}
	got, err := reachable(dir, []string{"./cmd/app"}, []string{"./lib"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"lib/lib.go#New", "lib/lib.go#Env.Run", "lib/lib.go#Env.Name", "lib/lib.go#helper", "lib/lib.go#name", "lib/lib.go#Wrap", "lib/lib.go#Box.Get"} {
		if !slices.Contains(got, want) {
			t.Errorf("%s missing from %v", want, got)
		}
	}
	for _, absent := range []string{"lib/lib.go#hidden.Visible", "lib/lib.go#unused", "lib/lib.go#unusedInts", "lib/lib.go#Pair.Swap"} {
		if slices.Contains(got, absent) {
			t.Errorf("%s is listed: %v", absent, got)
		}
	}
}

// A binary that a build tag selects is reachable only in a build with that tag; reachableUnion lists it next to the default build's functions.
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
	plain, err := reachableUnion(dir, []string{"."}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(plain, "main_tag.go#onlyTagged") {
		t.Fatalf("the default build lists a tagged function: %v", plain)
	}
	both, err := reachableUnion(dir, []string{"."}, nil, []string{"tagged"})
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
		one, err := reachable(dir, []string{"."}, nil, tags)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, one...)
	}
	slices.Sort(want)
	want = slices.Compact(want)
	got, err := reachableUnion(dir, []string{"."}, nil, []string{"a", "b"})
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
	if _, err := reachableUnion(dir, []string{"."}, nil, []string{"a", "broken"}); err == nil {
		t.Fatal("a build that does not compile did not fail the union")
	}
}
