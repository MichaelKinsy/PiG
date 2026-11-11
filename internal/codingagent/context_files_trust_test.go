package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func writeTrustContextFile(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pig additive (D211): a run that withholds the project keeps the agent directory's file and the home directory's file, and skips every other file the walk finds.
func TestWithheldProjectKeepsGlobalAndHomeContextFilesOnly(t *testing.T) {
	home := CanonicalizePath(t.TempDir())
	t.Setenv("HOME", home)
	agentDir := filepath.Join(home, ".pig", "agent")
	cwd := filepath.Join(home, "work", "project", "sub")
	global := writeTrustContextFile(t, agentDir, "global")
	homeFile := writeTrustContextFile(t, home, "home")
	parent := writeTrustContextFile(t, filepath.Join(home, "work"), "parent")
	project := writeTrustContextFile(t, filepath.Join(home, "work", "project"), "project")
	nearest := writeTrustContextFile(t, cwd, "nearest")

	paths := func(files []ContextFile) []string {
		var out []string
		for _, f := range files {
			out = append(out, f.Path)
		}
		return out
	}
	if got, want := paths(LoadProjectContextFilesScoped(cwd, agentDir, false)), []string{global, homeFile}; !slices.Equal(got, want) {
		t.Errorf("untrusted = %v, want %v", got, want)
	}
	if got, want := WithheldProjectContextFiles(cwd, agentDir), []string{parent, project, nearest}; !slices.Equal(got, want) {
		t.Errorf("withheld = %v, want %v", got, want)
	}
	if got, want := paths(LoadProjectContextFilesScoped(cwd, agentDir, true)), []string{global, homeFile, parent, project, nearest}; !slices.Equal(got, want) {
		t.Errorf("trusted = %v, want %v", got, want)
	}
}

// pig additive (D211): a project that is the home directory itself has its own file withheld.
func TestWithheldHomeProjectWithholdsItsOwnContextFile(t *testing.T) {
	home := CanonicalizePath(t.TempDir())
	t.Setenv("HOME", home)
	own := writeTrustContextFile(t, home, "home project")
	if got := LoadProjectContextFilesScoped(home, "", false); len(got) != 0 {
		t.Errorf("untrusted project at home loaded %v", got)
	}
	if got := WithheldProjectContextFiles(home, ""); !slices.Equal(got, []string{own}) {
		t.Errorf("withheld = %v, want [%s]", got, own)
	}
}

// Stock pig is unchanged (D211): a project whose only resource is a context file needs no trust decision, as in Pi.
func TestContextFileAloneIsNotTrustRequiringProjectResource(t *testing.T) {
	home := CanonicalizePath(t.TempDir())
	t.Setenv("HOME", home)
	cwd := filepath.Join(home, "work")
	writeTrustContextFile(t, cwd, "project")
	if HasTrustRequiringProjectResources(cwd) {
		t.Error("a project with only AGENTS.md needs no trust decision in stock pig")
	}
}
