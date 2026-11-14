//go:build !windows

package mcpext

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The MCP credential store writes through the shared replacement: a store file with a second hard link is written in place, so both names stay current.
func TestMcpCredentialStoreIsWrittenInPlaceThroughAHardLink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-auth.json")
	other := filepath.Join(dir, "second-name.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	inode := func(name string) uint64 {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return info.Sys().(*syscall.Stat_t).Ino
	}
	before := inode(path)
	next := "{\"server\": {\"token\": \"t\"}}"
	if err := NewFileAuthStorageBackend(path).WithLock(func(string, bool) (*string, error) { return &next, nil }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, other} {
		if got, _ := os.ReadFile(name); string(got) != next {
			t.Errorf("%s = %q, want %q", name, got, next)
		}
	}
	if inode(path) != before {
		t.Fatal("the hard link was broken")
	}
}
