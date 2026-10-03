package node

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
