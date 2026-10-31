package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
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
	for key, value := range manifest {
		fields[key] = value
	}
	text, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	all := map[string]string{"package.json": string(text)}
	for file, content := range files {
		all[file] = content
	}
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
