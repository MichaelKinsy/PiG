//go:build !windows

package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// The debug log sits in the temporary directory every local user can write: a symlink another user planted there must not
// redirect it, and the file PiG creates is private.
func TestDebugLogRefusesASymlinkAndIsPrivate(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("PIG_DEBUG", "1")
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "pig-debug.log")
	if err := os.Symlink(victim, log); err != nil {
		t.Fatal(err)
	}
	debugLog("planted %d", 1)
	if data, _ := os.ReadFile(victim); string(data) != "keep\n" {
		t.Fatalf("the debug log followed a symlink and wrote to its target: %q", data)
	}

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	debugLog("fresh %d", 2)
	info, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("debug log mode = %v, want 0600", info.Mode().Perm())
	}
}

// A hard link another user made to one of this user's files is a regular file this user owns, so ownership alone does not
// stop the log from appending to it; a log left readable by others is made private.
func TestDebugLogRefusesAHardLinkAndTightensAnOpenLog(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("PIG_DEBUG", "1")
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(tmp, "pig-debug.log")
	if err := os.Link(victim, log); err != nil {
		t.Skipf("hard link: %v", err)
	}
	debugLog("planted %d", 1)
	if data, _ := os.ReadFile(victim); string(data) != "keep\n" {
		t.Fatalf("the debug log wrote through a hard link: %q", data)
	}

	if err := os.Remove(log); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(log, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(log, 0o644); err != nil {
		t.Fatal(err)
	}
	debugLog("open %d", 2)
	info, err := os.Stat(log)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("debug log mode = %v, want 0600", info.Mode().Perm())
	}
}
