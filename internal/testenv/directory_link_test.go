package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi code sees a directory junction through Node, whose lstat reports it as a symbolic link, whose readdir types it as a link, and whose realpath resolves it. The fixture must look the same to Go code on every host.
func TestRequireDirectoryLinkIsASymbolicLinkToItsTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target & directory")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "marker"), []byte("linked"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	RequireDirectoryLink(t, target, link)

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lstat mode=%v; want a symbolic link", info.Mode())
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() == "link" && entry.Type()&os.ModeSymlink == 0 {
			t.Fatalf("directory entry type=%v; want a symbolic link", entry.Type())
		}
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(link, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(filepath.Join(target, "marker"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved != want {
		t.Fatalf("realpath=%q; want %q", resolved, want)
	}
	data, err := os.ReadFile(filepath.Join(link, "marker"))
	if err != nil || string(data) != "linked" {
		t.Fatalf("content through the link=%q err=%v", data, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "marker")); err != nil {
		t.Fatalf("removing the link removed its target: %v", err)
	}
}

// A relative target names a directory beside the link, as for a symbolic link and Node's junction, not one under the working directory.
func TestRequireDirectoryLinkResolvesRelativeTargetsFromTheLink(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested", "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "target", "marker"), []byte("relative"), 0o600); err != nil {
		t.Fatal(err)
	}
	RequireDirectoryLink(t, "target", filepath.Join(root, "nested", "link"))
	data, err := os.ReadFile(filepath.Join(root, "nested", "link", "marker"))
	if err != nil || string(data) != "relative" {
		t.Fatalf("content through the relative link=%q err=%v", data, err)
	}
}

func TestRequireDirectoryLinkFailsOnAnOccupiedPath(t *testing.T) {
	root := t.TempDir()
	if err := createDirectoryLink(root, root); err == nil {
		t.Fatal("linking over an existing directory succeeded")
	}
}
