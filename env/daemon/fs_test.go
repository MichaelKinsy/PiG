package daemon

// pi: packages/env/daemon/src/fs.rs

// Ports the Node fs rules of packages/env/daemon/src/fs.rs: rm without recursive on a directory is ERR_FS_EISDIR
// (fs.rs:160-173), rm --force ignores a missing path, fsync of a directory is EISDIR (fs.rs:139-146), mkdtemp makes
// prefix plus six characters (fs.rs:184-189), and rename and truncate report Node's messages.

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRmFollowsNodesFsRm(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")
	if _, failure := rm(missing, false, true); failure != nil {
		t.Fatalf("force on a missing path: %+v", failure)
	}
	if _, failure := rm(missing, false, false); failure == nil || failure.Code != "ENOENT" || failure.Syscall != "lstat" {
		t.Fatalf("missing path: %+v", failure)
	}
	if _, failure := rm(dir, false, false); failure == nil || failure.Code != "ERR_FS_EISDIR" ||
		failure.Message != "Path is a directory: rm returned EISDIR (is a directory) "+dir || failure.Path != dir {
		t.Fatalf("directory without recursive: %+v", failure)
	}
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, failure := rm(filepath.Join(dir, "a"), true, false); failure != nil {
		t.Fatalf("recursive: %+v", failure)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Fatalf("the directory is still there: %v", err)
	}
}

func TestFsyncRefusesADirectoryAsNodeDoes(t *testing.T) {
	dir := t.TempDir()
	_, failure := fsync(dir)
	if failure == nil || failure.Code != "EISDIR" || failure.Message != "EISDIR: illegal operation on a directory, open '"+dir+"'" {
		t.Fatalf("fsync of a directory: %+v", failure)
	}
}

func TestMkdtempAppendsSixRandomCharacters(t *testing.T) {
	prefix := filepath.Join(t.TempDir(), "pi-")
	result, failure := mkdtemp(prefix)
	if failure != nil {
		t.Fatal(failure)
	}
	path := result["path"].(string)
	if !regexp.MustCompile("^" + regexp.QuoteMeta(prefix) + "[A-Za-z0-9]{6}$").MatchString(path) {
		t.Fatalf("path %q", path)
	}
	if stats, err := os.Stat(path); err != nil || !stats.IsDir() {
		t.Fatalf("not a directory: %v", err)
	}
	_, failure = mkdtemp(filepath.Join(t.TempDir(), "missing", "pi-"))
	if failure == nil || failure.Code != "ENOENT" || failure.Syscall != "mkdtemp" || filepath.Base(failure.Path) != "pi-XXXXXX" {
		t.Fatalf("mkdtemp in a missing directory: %+v", failure)
	}
}

func TestRenameAndTruncateReportNodesMessages(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
	if _, failure := rename(from, to); failure == nil || failure.Message != "ENOENT: no such file or directory, rename '"+from+"' -> '"+to+"'" {
		t.Fatalf("rename: %+v", failure)
	}
	if err := os.WriteFile(from, []byte("abcdef"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, failure := truncate(from, 3); failure != nil {
		t.Fatal(failure)
	}
	if content, _ := os.ReadFile(from); string(content) != "abc" {
		t.Fatalf("truncated to %q", content)
	}
	if _, failure := truncate(to, 3); failure == nil || failure.Code != "ENOENT" || failure.Syscall != "open" {
		t.Fatalf("truncate of a missing file: %+v", failure)
	}
}
