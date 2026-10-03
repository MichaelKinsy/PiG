package nodeerrno

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// MkdirAll and FromPathError report failures as Node's fs.mkdirSync(dir, {recursive: true}), fs.writeFileSync and fs.readFileSync do on the same paths: a recursive mkdir names the requested path and reports EEXIST for a file in its place and ENOTDIR for a file above it; an open names its path; a read names none.
func TestMkdirAllAndFromPathErrorMatchNode(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	const script = `const fs = require('node:fs');
const [file, root] = process.argv.slice(1);
const path = require('node:path');
const run = (f) => { try { f(); return 'ok'; } catch (err) { return err.message; } };
console.log(JSON.stringify([
  run(() => fs.mkdirSync(file, { recursive: true })),
  run(() => fs.mkdirSync(path.join(file, 'a', 'b'), { recursive: true })),
  run(() => fs.mkdirSync(path.join(root, 'new', 'dir'), { recursive: true })),
  run(() => fs.writeFileSync(path.join(file, 'store.json'), '{}')),
  run(() => fs.readFileSync(root, 'utf-8')),
]));`
	out, err := exec.CommandContext(t.Context(), "node", "-e", script, file, root).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
	var node []string
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatalf("Node output %q: %v", out, err)
	}
	message := func(err error) string {
		if err == nil {
			return "ok"
		}
		return err.Error()
	}
	_, readErr := os.ReadFile(root)
	got := []string{
		message(MkdirAll(file, 0o700)),
		message(MkdirAll(filepath.Join(file, "a", "b"), 0o700)),
		message(MkdirAll(filepath.Join(root, "new", "dir"), 0o700)),
		message(FromPathError(os.WriteFile(filepath.Join(file, "store.json"), []byte("{}"), 0o600))),
		message(FromPathError(readErr)),
	}
	for i := range node {
		if got[i] != node[i] {
			t.Errorf("case %d: %q, Node reports %q", i, got[i], node[i])
		}
	}
}
