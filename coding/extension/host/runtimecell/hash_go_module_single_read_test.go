package runtimecell

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// openCountingFS counts the opens of each name: the reads of a file through fs.ReadFile, which uses Open here.
type openCountingFS struct {
	fs.FS
	mu    sync.Mutex
	opens map[string]int
}

func (c *openCountingFS) Open(name string) (fs.File, error) {
	c.mu.Lock()
	c.opens[name]++
	c.mu.Unlock()
	return c.FS.Open(name)
}

// writeSyntheticModule writes a module with each kind of file the walk treats differently and returns its root.
func writeSyntheticModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                "module example.com/m\n\ngo 1.26\n",
		"go.sum":                "",
		"root.go":               "package m\n\nimport _ \"embed\"\n\n//go:embed assets/*.txt\nvar assets string\n",
		"root_test.go":          "package m\n",
		"assets/a.txt":          "a",
		"tagged.go":             "//go:build linux\n\npackage m\n",
		"empty.go":              "",
		"notes.md":              "not go",
		"pkg/pkg.go":            "package pkg\n",
		"pkg/pkg_test.go":       "package pkg\n",
		"pkg/native.c":          "int x;",
		"real_target.go.txt":    "package m\n",
		"pkg/second.go":         "package pkg\n\nvar X = 1\n",
		"cmd/tool/main.go":      "package main\n",
		"cmd/tool/main_test.go": "package main\n",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		testenv.Symlink(t, "pkg/pkg.go", filepath.Join(root, "linked.go"))
	}
	return root
}

// goldenSyntheticDigest was taken from the walk as it was before it read each .go file once (it read each twice): run on commit 8ca87fa349 by the same test.
const goldenSyntheticDigest = "da4f6b5f32cebe9302a12e602ef19d7f373c4e0c20f273d93e2e39817bc9cb30"

func TestHashGoModuleDigestOfASyntheticTreeIsUnchanged(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the tree holds a symlink")
	}
	root := writeSyntheticModule(t)
	got := hashGoModuleFS(os.DirFS(root), "example.com/m")
	t.Logf("digest %s", got)
	if got != goldenSyntheticDigest {
		t.Fatalf("digest %s, want %s", got, goldenSyntheticDigest)
	}
}

func TestHashGoModuleReadsEachGoSourceOnce(t *testing.T) {
	root := writeSyntheticModule(t)
	counting := &openCountingFS{FS: os.DirFS(root), opens: map[string]int{}}
	if hash := hashGoModuleFS(counting, "example.com/m"); strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	for _, name := range []string{"root.go", "tagged.go", "empty.go", "pkg/pkg.go", "pkg/second.go", "cmd/tool/main.go"} {
		if counting.opens[name] != 1 {
			t.Errorf("%s was opened %d times, want 1", name, counting.opens[name])
		}
	}
	for _, name := range []string{"root_test.go", "pkg/pkg_test.go", "cmd/tool/main_test.go", "notes.md"} {
		if counting.opens[name] != 0 {
			t.Errorf("%s was opened %d times, want 0", name, counting.opens[name])
		}
	}
}

// refusingFS refuses to open one name with a permission error.
type refusingFS struct {
	fs.FS
	name string
}

func (r refusingFS) Open(name string) (fs.File, error) {
	if name == r.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
	}
	return r.FS.Open(name)
}

// A .go file that cannot be read is the walk's error, named by its path, as it was.
func TestHashGoModuleReportsAnUnreadableGoFile(t *testing.T) {
	root := writeSyntheticModule(t)
	got := hashGoModuleFS(refusingFS{FS: os.DirFS(root), name: "pkg/pkg.go"}, "example.com/m")
	t.Logf("result %s", got)
	if want := "error:open pkg/pkg.go: permission denied"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
