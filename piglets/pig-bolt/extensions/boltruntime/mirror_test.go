package boltruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteMirrorLaysOutCopiesAndLinksTheRest(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, content string) string {
		return writeSource(t, root, rel, content)
	}
	index := write("proj/ext/index.ts", "export default () => import.meta.url;")
	lib := write("proj/ext/lib/a.ts", "export const a = 1;")
	unused := write("proj/ext/lib/unused.ts", "export const unused = 2;")
	dep := write("proj/node_modules/dep/index.js", "module.exports = 1;")
	depPkg := write("proj/node_modules/dep/package.json", `{"name":"dep"}`)
	other := write("proj/node_modules/other/index.js", "module.exports = 2;")
	write("proj/sibling/x.ts", "export const x = 1;")
	pkg := write("proj/package.json", `{"type":"module"}`)

	stageDir := t.TempDir()
	rewritten := "export default () => \"file:///orig/index.ts\";"
	m, err := stage{dir: stageDir}.writeMirror([]string{index, lib, dep, depPkg}, map[string]string{index: rewritten})
	if err != nil {
		t.Fatal(err)
	}

	// An input is a file of its own: the rewritten source for a changed one, and never a path to write through into the original.
	if got, _ := os.ReadFile(m.place(index)); string(got) != rewritten {
		t.Errorf("copy of the changed file = %q", got)
	}
	if got, _ := os.ReadFile(index); !strings.Contains(string(got), "import.meta.url") {
		t.Errorf("the original of the changed file was written: %q", got)
	}
	if info, err := os.Lstat(m.place(lib)); err != nil || info.Mode()&os.ModeSymlink != 0 {
		t.Errorf("an unchanged input is a file of its own in the mirror: %v %v", info, err)
	}
	if got, _ := os.ReadFile(m.place(lib)); string(got) != "export const a = 1;" {
		t.Errorf("copy of the unchanged file = %q", got)
	}

	// What the bundler may look at but leaves out is the original, by a link: a file beside an input, a package no input names, a sibling directory and the package.json above.
	for _, original := range []string{unused, filepath.Dir(other), filepath.Join(root, "proj", "sibling"), pkg} {
		target, err := os.Readlink(m.place(original))
		if err != nil || target != original {
			t.Errorf("%s: Readlink = %q, %v; want a link to the original", original, target, err)
		}
	}
	if got := m.originalOf(m.place(unused)); got != unused {
		t.Errorf("originalOf(link to unused) = %q", got)
	}
	if got := m.originalOf(m.place(index)); got != index {
		t.Errorf("originalOf(copy) = %q", got)
	}
	if got := m.originalOf(index); got != index {
		t.Errorf("originalOf(an original) = %q", got)
	}
	if got, err := m.pathOf(index); err != nil || got != m.place(index) {
		t.Errorf("pathOf(index) = %q, %v", got, err)
	}
	if _, err := m.pathOf(unused); err == nil {
		t.Errorf("pathOf(a file that is no input) succeeded")
	}
}

// A directory above the extension's can hold far more than resolution reaches: past the bound only what the resolver looks for is linked.
func TestWriteMirrorBoundsTheLinksOfAHugeDirectory(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(root, "huge")
	if err := os.MkdirAll(filepath.Join(huge, "ext"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range maxMirroredEntries + 10 {
		if err := os.WriteFile(filepath.Join(huge, fmt.Sprintf("f%05d", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"node_modules", "package.json"} {
		if err := os.WriteFile(filepath.Join(huge, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	entry := writeSource(t, huge, "ext/index.ts", "export default 1;")
	m, err := stage{dir: t.TempDir()}.writeMirror([]string{entry}, nil)
	if err != nil {
		t.Fatal(err)
	}
	linked, err := os.ReadDir(m.place(huge))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range linked {
		names = append(names, e.Name())
	}
	if strings.Join(names, " ") != "ext node_modules package.json" {
		t.Errorf("the huge directory holds %d entries in the mirror, want ext, node_modules and package.json: %v", len(names), names[:min(len(names), 5)])
	}
}
