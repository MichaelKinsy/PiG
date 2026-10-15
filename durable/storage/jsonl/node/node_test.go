package node

// pi: packages/durable/src/storage/jsonl/node.ts

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	durableenv "github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/storage/jsonl"
)

// resolvePath (packages/durable/src/env/node.ts:58-71) expands "~" and "~/", converts a file:// URL with
// fileURLToPath, keeps a malformed URL as an ordinary path, and resolves the result against the environment's cwd.
func TestNodeFileSystemResolvesPathsAsResolvePathDoes(t *testing.T) {
	home := t.TempDir()
	// os.homedir() reads HOME on POSIX and USERPROFILE on Windows.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd := t.TempDir()
	fileSystem := &NodeFileSystem{Cwd: cwd}
	cases := []struct{ path, want string }{
		{"~", home},
		{"~/sessions/../store", filepath.Join(home, "store")},
		{"~user/store", filepath.Join(cwd, "~user", "store")},
		{"store/./main/", filepath.Join(cwd, "store", "main")},
	}
	if runtime.GOOS != "windows" {
		cases = append(cases, []struct{ path, want string }{
			{"file:///var/durable/a%20b", "/var/durable/a b"},
			// fileURLToPath rejects a host on POSIX, so the URL stays an ordinary relative path.
			{"file://remote/store", filepath.Join(cwd, "file:/remote/store")},
			{"/abs/../store", "/store"},
		}...)
	}
	for _, test := range cases {
		got, err := fileSystem.AbsolutePath(context.Background(), test.path)
		if err != nil {
			t.Fatalf("AbsolutePath(%q): %v", test.path, err)
		}
		if got != test.want {
			t.Errorf("AbsolutePath(%q) = %q, want %q", test.path, got, test.want)
		}
	}

	// Every file operation resolves its path the same way.
	if err := fileSystem.WriteFile(context.Background(), "~/written.jsonl", "line\n"); err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(home, "written.jsonl")); err != nil || string(content) != "line\n" {
		t.Fatalf("home file = %q, %v", content, err)
	}
}

// JsonlStorage reaches its files through the FileSystem members it names (packages/durable/src/storage/jsonl/storage.ts:2 imports FileSystem; :242 holds it,
// :262 and :786 take it). NodeFileSystem is the local implementation: through the narrowed jsonl.FileSystem interface it joins and resolves paths, creates a
// directory, writes, appends, truncates, flushes, reads, renames, lists and removes, each with the effect its upstream name states.
// Pi: packages/durable/src/storage/jsonl/storage.ts:242 (absolutePath, joinPath, readBinaryFile, writeFile, appendFile, truncateFile, flushFile, renameFile, listDir, createDir, remove).
func TestNodeFileSystemThroughTheJsonlFileSystemInterface(t *testing.T) {
	ctx := context.Background()
	cwd := t.TempDir()
	var fs jsonl.FileSystem = &NodeFileSystem{Cwd: cwd}

	joined, err := fs.JoinPath(ctx, []string{"store", "main"})
	if err != nil || joined != filepath.Join("store", "main") {
		t.Fatalf("JoinPath = %q, %v", joined, err)
	}
	abs, err := fs.AbsolutePath(ctx, joined)
	if err != nil || abs != filepath.Join(cwd, "store", "main") {
		t.Fatalf("AbsolutePath = %q, %v", abs, err)
	}
	if err := fs.CreateDir(ctx, abs, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(abs, "log.jsonl")
	if err := fs.WriteFile(ctx, file, "abc"); err != nil {
		t.Fatal(err)
	}
	if err := fs.AppendFile(ctx, file, []byte("def")); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadBinaryFile(ctx, file); err != nil || !bytes.Equal(got, []byte("abcdef")) {
		t.Fatalf("after write and append: %q, %v", got, err)
	}
	if err := fs.TruncateFile(ctx, file, 4); err != nil {
		t.Fatal(err)
	}
	if err := fs.FlushFile(ctx, file); err != nil {
		t.Fatal(err)
	}
	renamed := filepath.Join(abs, "main.jsonl")
	if err := fs.RenameFile(ctx, file, renamed); err != nil {
		t.Fatal(err)
	}
	if got, err := fs.ReadBinaryFile(ctx, renamed); err != nil || !bytes.Equal(got, []byte("abcd")) {
		t.Fatalf("after truncate and rename: %q, %v", got, err)
	}
	entries, err := fs.ListDir(ctx, abs)
	if err != nil || len(entries) != 1 || entries[0].Name != "main.jsonl" || entries[0].Size != 4 {
		t.Fatalf("ListDir = %+v, %v", entries, err)
	}
	if err := fs.Remove(ctx, renamed, &durableenv.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(renamed); !os.IsNotExist(err) {
		t.Fatalf("Remove left the file: %v", err)
	}
}
