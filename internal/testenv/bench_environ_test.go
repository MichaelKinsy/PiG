package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyPathIsOneEmptyDirectoryAndEndsWithTheTest(t *testing.T) {
	before := os.Getenv("PATH")
	var pinned string
	t.Run("pinned", func(t *testing.T) {
		EmptyPath(t)
		pinned = os.Getenv("PATH")
		if pinned == "" || strings.Contains(pinned, string(os.PathListSeparator)) {
			t.Fatalf("PATH = %q, want one directory", pinned)
		}
		entries, err := os.ReadDir(pinned)
		if err != nil || len(entries) != 0 {
			t.Fatalf("PATH directory %q: %d entries, %v; want an existing empty directory", pinned, len(entries), err)
		}
	})
	if got := os.Getenv("PATH"); got != before {
		t.Errorf("PATH after the test = %q, want %q", got, before)
	}
	if _, err := os.Stat(pinned); err == nil {
		t.Errorf("directory %q still exists after the test", pinned)
	}
}

func TestPathOfNamesOnlyTheDirectoriesOfTheProgramsAndKeepsTheRestOfTheEnvironment(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir, name := filepath.Dir(self), filepath.Base(self)
	t.Setenv("PIG_PATHOF_PROBE", "kept")
	t.Setenv("PATH", strings.Join([]string{filepath.Join(t.TempDir(), "missing"), dir, filepath.Join(t.TempDir(), "other")}, string(os.PathListSeparator)))
	before := os.Getenv("PATH")
	t.Run("pinned", func(t *testing.T) {
		PathOf(t, name, name)
		if got := os.Getenv("PATH"); got != filepath.Clean(dir) {
			t.Errorf("PATH = %q, want the one directory %q of the program", got, filepath.Clean(dir))
		}
		if got := os.Getenv("PIG_PATHOF_PROBE"); got != "kept" {
			t.Errorf("PIG_PATHOF_PROBE = %q, want it kept", got)
		}
	})
	if got := os.Getenv("PATH"); got != before {
		t.Errorf("PATH after the test = %q, want %q", got, before)
	}
}
