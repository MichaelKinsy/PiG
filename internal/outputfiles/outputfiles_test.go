package outputfiles

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

var pathPattern = regexp.MustCompile(`^pi-test-[0-9a-f]{16}\.txt$`)

func useTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	return dir
}

func requireUserOnly(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("%s mode = %o, want 600", path, got)
	}
}

func TestWriteFileIsUserOnlyInTheTempDirectory(t *testing.T) {
	dir := useTempDir(t)
	path, err := WriteFile("pi-test", ".txt", []byte("private"))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || !pathPattern.MatchString(filepath.Base(path)) {
		t.Errorf("path = %q, want %s/pi-test-<16 hex>.txt", path, dir)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "private" {
		t.Errorf("content = %q, %v", data, err)
	}
	requireUserOnly(t, path)
}

func TestCreateStreamIsUserOnlyAndWritable(t *testing.T) {
	useTempDir(t)
	stream := OpenStream("pi-test", ".txt")
	stream.WriteString("streamed")
	res := stream.Close()
	if !res.Saved() {
		t.Fatalf("Close() = %+v, want saved", res)
	}
	if data, err := os.ReadFile(res.Path); err != nil || string(data) != "streamed" {
		t.Errorf("content = %q, %v", data, err)
	}
	requireUserOnly(t, res.Path)
}

// `wx` never opens a file that exists, such as a link someone placed at a guessed path.
func TestCreateIsExclusive(t *testing.T) {
	dir := useTempDir(t)
	existing := filepath.Join(dir, "taken")
	if err := os.WriteFile(existing, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if f, err := create(existing); err == nil {
		_ = f.Close()
		t.Fatal("create replaced an existing file")
	}
	if data, _ := os.ReadFile(existing); string(data) != "keep" {
		t.Errorf("existing file = %q", data)
	}
}

func TestPathsAreUnique(t *testing.T) {
	useTempDir(t)
	seen := map[string]bool{}
	for range 100 {
		path := newPath("pi-test", ".txt")
		if seen[path] {
			t.Fatalf("duplicate path %q", path)
		}
		seen[path] = true
	}
}

// OpenStream captures open failures without panicking and drops the path on failure (Pi 943a99e3c).
func TestCreateStreamKeepsThePathWhenTheDirectoryIsMissing(t *testing.T) {
	dir := useTempDir(t)
	missing := filepath.Join(dir, "missing")
	t.Setenv("TMPDIR", missing)
	t.Setenv("TMP", missing)
	t.Setenv("TEMP", missing)
	stream := OpenStream("pi-test", ".txt")
	res := stream.Close()
	if res.Path != "" || !res.Failed() {
		t.Fatalf("Close() = %+v, want empty path and error", res)
	}
	if _, writeErr := WriteFile("pi-test", ".txt", nil); writeErr == nil {
		t.Fatal("WriteFile reported no error")
	}
}
