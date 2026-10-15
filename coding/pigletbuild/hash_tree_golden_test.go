//go:build !windows

package pigletbuild

import (
	"os"
	"path/filepath"
	"testing"
)

// Lock digests are recorded in build locks and compared on verify, so the hashed
// byte stream must not change. The values were computed by the whole-file
// implementation that preceded streaming. Windows reports other mode strings.
func TestHashTreeDigestsAreStable(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":            "module example.test/golden\n",
		"main.go":           "package main\n\nfunc main() {}\n",
		"sub/data.bin":      "\x00\x01\x02binary\xff",
		"sub/deeper/z.txt":  "",
		"node_modules/x.js": "ignored",
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tree, err := hashTree(root)
	if err != nil {
		t.Fatal(err)
	}
	single, err := hashTree(filepath.Join(root, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:dadb81d022a7b4a707915c681f625f8990b600a7588eb52a8f486e9826292f50"; tree != want {
		t.Errorf("hashTree(dir) = %s, want %s", tree, want)
	}
	if want := "sha256:364f13b840fb692e3b299d15e4b7b8000be6104edb779e9ad8d1e3f48fc5801f"; single != want {
		t.Errorf("hashTree(file) = %s, want %s", single, want)
	}
}
