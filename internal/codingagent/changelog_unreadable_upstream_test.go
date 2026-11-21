package codingagent

// pi: packages/coding-agent/src/utils/changelog.ts

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// printedWhile runs fn with the process's stdout and stderr replaced by pipes and returns what fn wrote to each: the real streams a direct terminal write would use.
func printedWhile(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	func() {
		defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()
		fn()
	}()
	_ = outW.Close()
	_ = errW.Close()
	o, _ := io.ReadAll(outR)
	e, _ := io.ReadAll(errR)
	_ = outR.Close()
	_ = errR.Close()
	return string(o), string(e)
}

// Ports packages/coding-agent/test/changelog.test.ts "parseChangelog" "returns no entries for an unreadable changelog without printing" (Pi ac78a6068b38, #12117, #11264):
// a directory passed as the changelog path (it exists and cannot be read as a file) yields no entries, and nothing is printed. Pi's parseChangelog(path) is parseChangelogFile here.
// PiG never printed the warning (this function returned nil at the failed read), so this test passes on the code before Pi's commit: it pins the behaviour, it is not a red.
func TestParseChangelogFileUnreadableReturnsNoEntriesWithoutPrinting(t *testing.T) {
	dir := t.TempDir()
	var entries []ChangelogEntry
	stdout, stderr := printedWhile(t, func() { entries = parseChangelogFile(dir) })
	if len(entries) != 0 {
		t.Fatalf("entries = %+v, want none", entries)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("printed stdout %q stderr %q, want nothing", stdout, stderr)
	}
	missing := filepath.Join(dir, "absent", "CHANGELOG.md")
	if got := parseChangelogFile(missing); len(got) != 0 {
		t.Fatalf("a missing changelog: entries = %+v, want none", got)
	}
}

// Pi ac78a6068b38 (interactive-mode-extension-changelog.test.ts is unchanged and passes): an extension package whose changelog cannot be read shows no startup note, draws nothing
// on the terminal and leaves the recorded version alone; /changelog with no entries shows "No changelog entries found.". PiG's own changelog is embedded in the binary, so only
// an extension package's can be unreadable at startup.
func TestExtensionChangelogUnreadableShowsNoNotesAndPrintsNothing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "pkg")
	if err := os.MkdirAll(filepath.Join(root, "CHANGELOG.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pi-demo-extension","version":"1.2.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	info := PiSourceInfo{
		Path:          filepath.Join(root, "a.ts"),
		Source:        "npm:pi-demo-extension",
		Scope:         "user",
		Origin:        "package",
		BaseDir:       root,
		ChangelogPath: filepath.Join(root, "CHANGELOG.md"),
	}
	sm := NewInMemorySettingsManager(Settings{})
	var updates []extensionChangelogUpdate
	_, stderr := printedWhile(t, func() {
		// The first launch records the version; the second finds a changelog it cannot read.
		getExtensionChangelogUpdates(sm, []PiSourceInfo{info}, false)
		if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"name":"pi-demo-extension","version":"1.3.0"}`), 0o644); err != nil {
			t.Error(err)
		}
		updates = getExtensionChangelogUpdates(sm, []PiSourceInfo{info}, false)
	})
	if len(updates) != 0 {
		t.Fatalf("updates = %+v, want none", updates)
	}
	if stderr != "" {
		t.Fatalf("printed on stderr %q, want nothing", stderr)
	}
	if got := FormatChangelogForChat(parseChangelogFile(info.ChangelogPath)); got != "No changelog entries found." {
		t.Fatalf("/changelog over an unreadable changelog = %q, want %q", got, "No changelog entries found.")
	}
}
