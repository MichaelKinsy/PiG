//go:build !windows

package mcpext

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

const mcpSeed = "{\n  \"mcpServers\": {\n    \"files\": {\"command\": \"npx\", \"args\": [\"-y\", \"@scope/files\"]}\n  }\n}\n"

func addServer(path string) error {
	_, err := AddMcpServerConfigJSON(path, "added", []byte(`{"command":"npx","args":["-y","@scope/added-server"]}`))
	return err
}

func mcpInode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func controlMcp(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, []byte(mcpSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := addServer(path); err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(path)
	if string(want) == mcpSeed {
		t.Fatal("the control write changed nothing")
	}
	return want
}

// mcp.json takes no lock file, so a directory the process cannot write holds a file it can write: the server is added in place, as Pi writes it, with the bytes a plain file ends with. A file with a second hard link is written in place too.
func TestMcpConfigIsWrittenInPlaceWhereAReplacementWouldChangeTheFile(t *testing.T) {
	want := controlMcp(t)
	t.Run("read-only directory", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes into any directory")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		if err := os.WriteFile(path, []byte(mcpSeed), 0o644); err != nil {
			t.Fatal(err)
		}
		before := mcpInode(t, path)
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		err := addServer(path)
		_ = os.Chmod(dir, 0o755)
		if err != nil {
			t.Fatalf("adding a server in a read-only directory: %v", err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			t.Fatalf("mcp.json = %q, want %q", got, want)
		}
		if mcpInode(t, path) != before {
			t.Fatal("the file was replaced in a directory that cannot be written")
		}
	})
	t.Run("hard link", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "mcp.json")
		other := filepath.Join(dir, "second-name.json")
		if err := os.WriteFile(path, []byte(mcpSeed), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Link(path, other); err != nil {
			t.Skipf("hard links are not available here: %v", err)
		}
		before := mcpInode(t, path)
		if err := addServer(path); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{path, other} {
			if got, _ := os.ReadFile(name); !bytes.Equal(got, want) {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if mcpInode(t, path) != before {
			t.Fatal("the hard link was broken")
		}
	})
}

// The MCP credential store writes through the same replacement: a store file with a second hard link keeps both names current.
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
	before := mcpInode(t, path)
	next := "{\"server\": {\"token\": \"t\"}}"
	if err := NewFileAuthStorageBackend(path).WithLock(func(string, bool) (*string, error) { return &next, nil }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, other} {
		if got, _ := os.ReadFile(name); string(got) != next {
			t.Errorf("%s = %q, want %q", name, got, next)
		}
	}
	if mcpInode(t, path) != before {
		t.Fatal("the hard link was broken")
	}
}
