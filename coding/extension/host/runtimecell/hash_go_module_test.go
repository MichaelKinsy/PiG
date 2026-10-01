package runtimecell

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// goModuleFiles is a module with every kind of path `go build` treats differently. The map value is the file content.
func goModuleFiles() map[string]string {
	return map[string]string{
		"go.mod":                        "module example.com/m\n\ngo 1.26\n",
		"go.sum":                        "",
		"root.go":                       "package m\n\nimport _ \"embed\"\n\n//go:embed assets/*.txt data\nvar assets string\n",
		"root_test.go":                  "package m\n",
		"assets/a.txt":                  "a",
		"assets/skip.md":                "not matched by the pattern",
		"data/nested/deep.bin":          "deep",
		"data/.hidden":                  "hidden files are not embedded from a directory",
		"data/_underscore":              "nor are underscore files",
		"pkg/pkg.go":                    "package pkg\n",
		"pkg/pkg_test.go":               "package pkg\n",
		"pkg/native.c":                  "int x;",
		"pkg/native.h":                  "extern int x;",
		"pkg/README.md":                 "not read by go build",
		"docs/guide.md":                 "not go source",
		"tmp/test-fixtures/fixture":     "build output",
		"testdata/fixture.go":           "package ignored\n",
		"_ignored/x.go":                 "package ignored\n",
		".hidden/x.go":                  "package ignored\n",
		"vendor/v/v.go":                 "package ignored\n",
		"nested/go.mod":                 "module example.com/nested\n\ngo 1.26\n",
		"nested/nested.go":              "package nested\n",
		"pkg/testdata/golden.txt":       "golden",
		"pkg/sub/internal_only_test.go": "package sub\n",
	}
}

func hashModule(t *testing.T, files map[string]string) string {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, content := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(content)}
	}
	hash := hashGoModuleFS(fsys, "example.com/m")
	if strings.HasPrefix(hash, "error:") {
		t.Fatal(hash)
	}
	return hash
}

// The identity covers what `go build` reads from a module: go.mod, go.sum, the non-test Go and cgo/assembly sources of every package directory, and the files its //go:embed patterns select. Everything else, including nested modules, testdata, vendor, and `.`/`_` directories, does not change the key.
func TestHashGoModuleCoversExactlyTheBuildSourceSet(t *testing.T) {
	base := hashModule(t, goModuleFiles())
	for _, path := range []string{"go.mod", "go.sum", "root.go", "assets/a.txt", "data/nested/deep.bin", "pkg/pkg.go", "pkg/native.c", "pkg/native.h"} {
		changed := goModuleFiles()
		changed[path] += " changed"
		if hashModule(t, changed) == base {
			t.Errorf("changing %s kept the module hash", path)
		}
	}
	for _, path := range []string{"root_test.go", "pkg/pkg_test.go", "assets/skip.md", "data/.hidden", "data/_underscore", "pkg/README.md", "docs/guide.md", "tmp/test-fixtures/fixture", "testdata/fixture.go", "_ignored/x.go", ".hidden/x.go", "vendor/v/v.go", "nested/go.mod", "nested/nested.go", "pkg/testdata/golden.txt", "pkg/sub/internal_only_test.go"} {
		changed := goModuleFiles()
		changed[path] += " changed"
		if got := hashModule(t, changed); got != base {
			t.Errorf("changing %s changed the module hash", path)
		}
		if path == "nested/go.mod" {
			// Without its go.mod the directory is a package of this module.
			continue
		}
		removed := goModuleFiles()
		delete(removed, path)
		if got := hashModule(t, removed); got != base {
			t.Errorf("removing %s changed the module hash", path)
		}
	}
	added := goModuleFiles()
	added["tmp/test-fixtures/rust-target/release/new-artifact"] = "created by another test"
	if got := hashModule(t, added); got != base {
		t.Errorf("a new file outside any package changed the module hash")
	}
	newPackage := goModuleFiles()
	newPackage["tmp/pkg/x.go"] = "package x\n"
	if got := hashModule(t, newPackage); got == base {
		t.Errorf("a new Go package in tmp/ did not change the module hash")
	}
}

// vanishingFS lists entries that another process deletes between the directory read and the next operation on them, as a test or build step that creates and removes files under the module does.
type vanishingFS struct {
	fs.FS
	dir      string
	phantoms []fs.DirEntry
	listed   int
}

func (v *vanishingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(v.FS, name)
	if err != nil {
		return nil, err
	}
	if name == v.dir {
		v.listed++
		entries = append(entries, v.phantoms...)
	}
	return entries, nil
}

func (v *vanishingFS) Open(name string) (fs.File, error) {
	for _, phantom := range v.phantoms {
		if filepath.Base(name) == phantom.Name() && filepath.Dir(name) == filepath.FromSlash(v.dir) {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
	}
	if filepath.Base(name) == "gone-dir" {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return v.FS.Open(name)
}

type phantomEntry struct {
	name string
	dir  bool
}

func (p phantomEntry) Name() string { return p.name }
func (p phantomEntry) IsDir() bool  { return p.dir }
func (p phantomEntry) Type() fs.FileMode {
	if p.dir {
		return fs.ModeDir
	}
	return 0
}
func (p phantomEntry) Info() (fs.FileInfo, error) {
	return nil, &fs.PathError{Op: "lstat", Path: p.name, Err: fs.ErrNotExist}
}

// An entry that is listed and then deleted must not fail the hash or change it: it was never source. The Rust fixture build under tmp/test-fixtures/rust-target does exactly this to a module whose root contains tmp/.
func TestHashGoModuleToleratesEntriesThatVanishDuringTraversal(t *testing.T) {
	files := goModuleFiles()
	base := fstest.MapFS{}
	for name, content := range files {
		base[name] = &fstest.MapFile{Data: []byte(content)}
	}
	want := hashGoModuleFS(base, "example.com/m")
	if strings.HasPrefix(want, "error:") {
		t.Fatal(want)
	}
	vanishing := &vanishingFS{FS: base, dir: "tmp/test-fixtures", phantoms: []fs.DirEntry{phantomEntry{name: "gone-file"}, phantomEntry{name: "gone-dir", dir: true}}}
	if got := hashGoModuleFS(vanishing, "example.com/m"); got != want {
		t.Fatalf("hash with vanishing entries = %q, want %q", got, want)
	}
	if vanishing.listed == 0 {
		t.Fatal("the traversal never listed the directory the entries vanish from")
	}
}

// A package directory that cannot be read is not a vanished entry: the key must fail closed.
func TestHashGoModuleFailsClosedOnUnreadablePackageFile(t *testing.T) {
	base := fstest.MapFS{
		"go.mod":  &fstest.MapFile{Data: []byte("module example.com/m\n")},
		"root.go": &fstest.MapFile{Data: []byte("package m\n")},
	}
	vanishing := &vanishingFS{FS: base, dir: ".", phantoms: []fs.DirEntry{phantomEntry{name: "gone.go"}}}
	if got := hashGoModuleFS(vanishing, "example.com/m"); !strings.HasPrefix(got, "error:") {
		t.Fatalf("a listed package source that cannot be opened produced reusable hash %q", got)
	}
}

// The hash of an on-disk module is stable while another goroutine creates and deletes files under a non-package directory of the module, the way the repository's tmp/ fixtures churn during a parallel test run.
func TestHashGoModuleIgnoresConcurrentChurnOutsidePackages(t *testing.T) {
	root := t.TempDir()
	for name, content := range goModuleFiles() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := hashGoModule(root)
	if strings.HasPrefix(want, "error:") {
		t.Fatal(want)
	}
	churn := filepath.Join(root, "tmp", "test-fixtures", "rust-target")
	stop, done := make(chan struct{}), make(chan error, 1)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			dir := filepath.Join(churn, "release", "deps")
			file := filepath.Join(dir, "artifact")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				done <- err
				return
			}
			if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
				done <- err
				return
			}
			if err := os.RemoveAll(filepath.Join(churn, "release")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				done <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := hashGoModule(root); got != want {
			close(stop)
			<-done
			t.Fatalf("hash changed or failed during churn: %q, want %q", got, want)
		}
	}
	close(stop)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The packed-cell key of an extension whose workspace module is a repository checkout tracks that module's Go source set and not its tmp/ build output or documentation.
func TestGoPackedCellHashTracksWorkspaceModuleSourceSetOnly(t *testing.T) {
	workspace := t.TempDir()
	sdkRoot := t.TempDir()
	write := func(root, name, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sdkRoot, "go.mod", "module github.com/MichaelKinsy/PiG/extensions/sdk\n\ngo 1.26\n")
	write(sdkRoot, "sdk.go", "package sdk\n")
	write(workspace, "go.mod", "module example.com/workspace\n\ngo 1.26\n")
	write(workspace, "workspace.go", "package workspace\n")
	extensions := []GoExtension{{Name: "extension", Root: workspace, ModulePath: "example.com/workspace", Package: "example.com/workspace", Factory: "Extension", Hash: "source", WorkspaceModules: []string{workspace}}}
	before := requireGoPackedHash(t, "packed-go", extensions, sdkRoot)
	write(workspace, "tmp/test-fixtures/rust-target/release/artifact", "build output")
	write(workspace, "docs/guide.md", "documentation")
	if after := requireGoPackedHash(t, "packed-go", extensions, sdkRoot); after != before {
		t.Fatalf("non-source files changed the packed cell key: %q, want %q", after, before)
	}
	write(workspace, "workspace.go", "package workspace\n\nconst changed = true\n")
	if after := requireGoPackedHash(t, "packed-go", extensions, sdkRoot); after == before {
		t.Fatal("a Go source change kept the packed cell key")
	}
}
