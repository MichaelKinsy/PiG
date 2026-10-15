//go:build !windows

package realpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// walk is filepath.EvalSymlinks for an absolute path: the same result, or an error where EvalSymlinks fails.
func TestWalkMatchesEvalSymlinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdir := func(rel string) {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := func(target, rel string) {
		if err := os.Symlink(target, filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
	}
	mkdir("a/b/c")
	mkdir("d")
	if err := os.WriteFile(filepath.Join(root, "a/file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	link("a/b", "rel")                         // relative link
	link(filepath.Join(root, "a/b/c"), "abs")  // absolute link
	link("../a/b", "d/up")                     // relative link with ".."
	link("rel", "chain")                       // link to a link
	link("missing", "dangling")                // dangling link
	link("loop2", "loop1")                     // link loop
	link("loop1", "loop2")                     //
	link("..", "a/b/c/parent")                 // link to ".."
	link(filepath.Join(root, "a"), "d/absdir") // absolute link inside a directory
	for _, rel := range []string{
		"", "a", "a/b/c", "a/b/c/", "a//b", "a/./b", "a/b/../b/c", "a/..", "..", "../..",
		"rel", "rel/c", "abs", "abs/..", "d/up/c", "chain/c", "chain/c/parent", "chain/c/parent/c/parent/..",
		"d/absdir/b", "d/absdir/../d/up", "a/file", "a/file/x", "a/missing", "dangling", "dangling/x", "loop1", "loop1/x",
	} {
		path := root + "/" + rel
		want, wantErr := filepath.EvalSymlinks(path)
		got, gotErr := walk(path)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("walk(%q) = %q, %v; EvalSymlinks gives %q, %v", path, got, gotErr, want, wantErr)
		}
	}
	for _, path := range []string{"/", "//", "/.", "/..", "/../tmp"} {
		want, wantErr := filepath.EvalSymlinks(path)
		got, gotErr := walk(path)
		if got != want || (gotErr == nil) != (wantErr == nil) {
			t.Errorf("walk(%q) = %q, %v; EvalSymlinks gives %q, %v", path, got, gotErr, want, wantErr)
		}
	}
}

// Canonicalizing a working directory costs one allocation per path component and two for the result, not a FileInfo and a
// path string per component.
func TestRealpathAllocatesOncePerComponent(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "a", "b", "c", "d", "e", "f", "g", "h")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	components := float64(strings.Count(deep, "/"))
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := walk(deep); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > components+2 {
		t.Errorf("walk of a %v-component path allocates %v times, want at most %v", components, allocs, components+2)
	}
}
