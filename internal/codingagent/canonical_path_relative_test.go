package codingagent

import (
	"os"
	"path/filepath"
	"testing"
)

// Pi utils/paths.ts:28-34 uses realpathSync, which returns an absolute path even for a relative input, and preserves the raw input on failure.
func TestCanonicalizePathRelativeMatchesPi(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFile(t, filepath.Join(dir, "session.jsonl"), "hello")
	want := realPathForTest(t, filepath.Join(dir, "session.jsonl"))
	for _, canonicalize := range []struct {
		name string
		call func(string) string
	}{{"public", CanonicalizePath}, {"shared", canonicalizePath}} {
		t.Run(canonicalize.name, func(t *testing.T) {
			for _, input := range []string{"session.jsonl", "./session.jsonl", want} {
				if got := canonicalize.call(input); got != want {
					t.Errorf("canonicalize(%q) = %q, want %q", input, got, want)
				}
			}
			for _, input := range []string{"", "missing.jsonl", "./missing.jsonl"} {
				if got := canonicalize.call(input); got != input {
					t.Errorf("missing canonicalize(%q) = %q, want raw input", input, got)
				}
			}
		})
	}
}

func TestSessionSelectorCurrentSessionRelativePath(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeContextFile(t, filepath.Join(dir, "current.jsonl"), "hello")
	current := SessionInfo{Path: filepath.Join(dir, "current.jsonl"), Name: "Current"}
	loader := func() ([]SessionInfo, error) { return []SessionInfo{current}, nil }
	sel := newLoadedSessionSelector(loader, loader, nil, nil, "current.jsonl", sessionSelectorInputBindings(t))
	if sel.currentPath != canonicalSessionPath(current.Path) {
		t.Fatalf("active relative path %q does not match listed absolute path %q", sel.currentPath, current.Path)
	}
	sel.HandleInput("\x04")
	if sel.confirmDelete != "" || sel.statusState.message != "Cannot delete the currently active session" {
		t.Fatalf("active session deletion allowed: confirmation=%q status=%q", sel.confirmDelete, sel.statusState.message)
	}
}

// package-manager.ts getNpmInstallArgs and uninstallNpm call realpathSync, which throws where CanonicalizePath keeps the raw input, and returns
// the absolute path for a relative one.
func TestRealPathSyncMatchesNodeRealpathSync(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	if err := os.Mkdir(filepath.Join(dir, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := realPathForTest(t, filepath.Join(dir, "npm"))
	for _, input := range []string{"npm", "./npm", want} {
		got, err := RealPathSync(input)
		if err != nil || got != want {
			t.Errorf("RealPathSync(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if got, err := RealPathSync("missing"); err == nil {
		t.Errorf("RealPathSync(missing) = %q, nil; want the error Node throws", got)
	}
}
