package plugins

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Ports packages/chord/test/next-node/plugins/support.ts (FIXTURES, KINDS, tempRoot, writeFiles, writePlugin). The host
// packages (`HOST_PACKAGES`, `kindsOf`) and `writePackage` serve the esbuild and `vm` tests, which this package replaces
// by the Builder and Evaluator seams (plugins_build.go, plugins_evaluate.go).

// fixtures is the fixture plugins' directory.
const fixtures = "testdata/plugins"

// kinds are the facet kinds of the fixtures' app.
var kinds = []string{"server", "worker", "web"}

// tempRoot is a temporary directory removed after the test, with fixture plugins (names of directories of testdata) copied
// into it.
func tempRoot(t *testing.T, names ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		if err := os.CopyFS(filepath.Join(root, name), os.DirFS(filepath.Join(fixtures, name))); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// writeFiles writes files (relative path to text) into dir, creating directories.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for path, text := range files {
		full := filepath.Join(dir, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// writePlugin writes a plugin named name into root/dir: its package.json (with manifest's fields) and files.
func writePlugin(t *testing.T, root, dir, name string, files map[string]string, manifest map[string]any) string {
	t.Helper()
	path := filepath.Join(root, dir)
	fields := map[string]any{"name": name, "type": "module"}
	maps.Copy(fields, manifest)
	text, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"package.json": string(text)}
	maps.Copy(all, files)
	writeFiles(t, path, all)
	return path
}

// realOf is the real path of path.
func realOf(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

// eventually polls until check holds, failing after timeout: vitest's `vi.waitFor(..., { timeout })`.
func eventually(t *testing.T, timeout time.Duration, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !check() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", timeout, what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// settler is the barrier of a component's watcher: a directory outside its roots, watched by its fsnotify watcher, where a
// file is written to learn that the events of everything written before it were handled.
type settler struct {
	dir     string
	watcher *fsnotify.Watcher
	seen    chan string
	count   int
}

var settlers sync.Map

// settle returns once the component's watcher handled the events of every file operation made before the call: a watch event
// of an edit arrives on its own time, and one that arrives after a build that followed the first event of the edit would leave
// the plugin dirty. A write in place is two events, a copied directory many. Nothing is waited for when the component does
// not watch (no first use yet, or a watcher that failed).
func settle(t *testing.T, plugins *Plugins) {
	t.Helper()
	found, _ := settlers.LoadOrStore(plugins, &settler{dir: t.TempDir(), seen: make(chan string, 1024)})
	barrier := found.(*settler)
	var watching bool
	plugins.section(func() {
		fs := plugins.watcher.fs
		if fs == nil {
			return
		}
		watching = true
		if barrier.watcher != fs {
			barrier.watcher = fs
			if err := fs.Add(barrier.dir); err != nil {
				t.Error(err)
			}
			plugins.watcher.observed = func(path string) { barrier.seen <- path }
		}
	})
	if !watching {
		return
	}
	barrier.count++
	sentinel := filepath.Join(barrier.dir, "sentinel-"+strconv.Itoa(barrier.count))
	if err := os.WriteFile(sentinel, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(15 * time.Second)
	for {
		select {
		case path := <-barrier.seen:
			if path == sentinel {
				return
			}
		case <-timeout:
			t.Fatal("the watcher did not handle a file written after the edits")
		}
	}
}
