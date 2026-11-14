//go:build !windows

package codingagent

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// inPlaceRows are the files of the agent directory pig writes, each with a writer to run on a seeded file. lockFree rows take no lock file in the directory, so a directory that cannot be written stops them only at the temporary file: Pi writes those in place.
func inPlaceRows() map[string]struct {
	file, seed string
	lockFree   bool
	run        func(agentDir string)
} {
	rows := map[string]struct {
		file, seed string
		lockFree   bool
		run        func(agentDir string)
	}{
		"settings.json (a setting change)": {
			file: "settings.json",
			seed: "{\n  \"theme\": \"dark\",\n  \"defaultProvider\": \"anthropic\"\n}\n",
			run: func(agentDir string) {
				manager := NewSettingsManager(filepath.Join(agentDir, "project"), agentDir)
				_ = manager.SetTheme("light")
				_ = manager.Flush()
			},
		},
		"trust.json (a trust decision)": {
			file: "trust.json",
			seed: "{\n  \"/earlier/project\": true\n}\n",
			run: func(agentDir string) {
				yes := true
				_ = NewProjectTrustStore(agentDir).Set("/probe/project", &yes)
			},
		},
	}
	for name, row := range stateFileRows {
		rows[name] = struct {
			file, seed string
			lockFree   bool
			run        func(agentDir string)
		}{file: row.file, seed: row.seed, lockFree: name != "apiKeys migration of settings.json", run: row.run}
	}
	return rows
}

func rowNames(rows map[string]struct {
	file, seed string
	lockFree   bool
	run        func(agentDir string)
}) []string {
	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func seedRow(t *testing.T, file, seed string) (dir, path string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, file)
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path
}

// controlBytes is what the writer leaves in a plain file on a plain directory.
func controlBytes(t *testing.T, file, seed string, run func(string)) []byte {
	t.Helper()
	dir, path := seedRow(t, file, seed)
	run(dir)
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) == seed {
		t.Fatalf("the writer changed nothing in %s", file)
	}
	return want
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

// A state file with a second hard link is written in place, as Pi writes it: replacing it would leave the other name with the old bytes. The bytes are those a plain file ends with.
func TestStateFilesWithAHardLinkAreWrittenInPlace(t *testing.T) {
	rows := inPlaceRows()
	for _, name := range rowNames(rows) {
		row := rows[name]
		want := controlBytes(t, row.file, row.seed, row.run)
		dir, path := seedRow(t, row.file, row.seed)
		other := path + ".second-name"
		if err := os.Link(path, other); err != nil {
			t.Skipf("hard links are not available here: %v", err)
		}
		before := inodeOf(t, path)
		row.run(dir)
		for _, file := range []string{path, other} {
			if got, _ := os.ReadFile(file); !bytes.Equal(got, want) {
				t.Errorf("%s: %s is %d bytes %q, want the %d bytes %q a plain file ends with", name, filepath.Base(file), len(got), got, len(want), want)
			}
		}
		if inodeOf(t, path) != before {
			t.Errorf("%s: the file was replaced although it has a second hard link", name)
		}
		noTemporaryFiles(t, dir)
	}
}

// A directory the process cannot write into holds a file it can: Pi writes the lock-free files (keybindings.json, its start-up migration) in place and they work; a temporary file there is refused, and the write goes in place. The migration is skipped without a message, so it must really run.
func TestLockFreeStateFilesAreWrittenInPlaceInAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes into any directory")
	}
	rows := inPlaceRows()
	for _, name := range rowNames(rows) {
		row := rows[name]
		if !row.lockFree {
			continue
		}
		want := controlBytes(t, row.file, row.seed, row.run)
		dir, path := seedRow(t, row.file, row.seed)
		before := inodeOf(t, path)
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		row.run(dir)
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			t.Errorf("%s: %s in a read-only directory is %d bytes %q, want the %d bytes %q a plain file ends with", name, row.file, len(got), got, len(want), want)
		}
		if inodeOf(t, path) != before {
			t.Errorf("%s: the file was replaced in a directory that cannot be written", name)
		}
		noTemporaryFiles(t, dir)
	}
}

// The in-place write is one line in the debug log (PIG_DEBUG), naming the file and why: a user who debugs a slow or odd write can see it; it is not an error and not a warning in the conversation.
func TestInPlaceWriteIsOneLineInTheDebugLog(t *testing.T) {
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("PIG_DEBUG", "1")
	dir, path := seedRow(t, "settings.json", "{\"theme\":\"dark\"}\n")
	if err := os.Link(path, path+".second-name"); err != nil {
		t.Skipf("hard links are not available here: %v", err)
	}
	manager := NewSettingsManager(filepath.Join(dir, "project"), dir)
	if err := manager.SetTheme("light"); err != nil {
		t.Fatal(err)
	}
	_ = manager.Flush()
	if drained := manager.DrainErrors(); len(drained) != 0 {
		t.Fatalf("the in-place write was reported as an error: %v", drained)
	}
	logged, err := os.ReadFile(filepath.Join(scratch, "pig-debug.log"))
	if err != nil {
		t.Fatalf("no debug log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logged)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], path) || !strings.Contains(lines[0], "hard link") {
		t.Fatalf("debug log = %q, want one line naming %s and the hard link", logged, path)
	}
}
