package mcpext_test

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/mcpext"
)

// The credential file is replaced whole: no temporary file is left behind, a new file is private (0600) and an existing file keeps its mode
// (Pi's writeFileSync).
func TestFileAuthStorageBackendWritesAtomicallyAndKeepsTheFileMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-auth.json")
	backend := mcpext.NewFileAuthStorageBackend(path)
	write := func(content string) {
		t.Helper()
		err := backend.WithLock(func(string, bool) (*string, error) { return &content, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	write(`{"a":1}`)
	if data, _ := os.ReadFile(path); string(data) != `{"a":1}` {
		t.Fatalf("content = %q", data)
	}
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Fatalf("new file mode = %v", info.Mode().Perm())
		}
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"a":2}`)
	if runtime.GOOS != "windows" {
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
			t.Fatalf("existing file mode = %v", info.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "mcp-auth.json" && e.Name() != "mcp-auth.json.lock" && !e.IsDir() {
			t.Fatalf("left behind: %s", e.Name())
		}
	}
}

// The file is replaced, not rewritten in place: a reader that holds the old file keeps the old bytes.
func TestFileAuthStorageBackendReplacesTheFileInsteadOfRewritingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("an open file cannot be replaced on Windows")
	}
	path := filepath.Join(t.TempDir(), "mcp-auth.json")
	backend := mcpext.NewFileAuthStorageBackend(path)
	write := func(content string) {
		t.Helper()
		if err := backend.WithLock(func(string, bool) (*string, error) { return &content, nil }); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"a":1}`)
	old, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	write(`{"a":2}`)
	data, err := io.ReadAll(old)
	if err != nil || string(data) != `{"a":1}` {
		t.Fatalf("the old file reads %q, %v", data, err)
	}
	if data, _ := os.ReadFile(path); string(data) != `{"a":2}` {
		t.Fatalf("content = %q", data)
	}
}
