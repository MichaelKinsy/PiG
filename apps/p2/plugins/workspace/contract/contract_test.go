package contract

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/oracle"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
)

// Contract fingerprints equal Pi's (see sessions/contract).
func TestWorkspaceContractsFingerprintAsPi(t *testing.T) {
	const module = "plugins/workspace/contract.ts"
	pi := oracle.Fingerprints(t, module)
	for name, contract := range map[string]*contracts.Contract{
		"WorkspaceWrites": WorkspaceWrites, "WorkspaceBackend": WorkspaceBackend, "Workspace": Workspace, "WorkspaceTransfer": WorkspaceTransfer,
	} {
		want, ok := pi[module+"#"+name]
		if !ok {
			t.Errorf("%s: Pi exports no such contract", name)
			continue
		}
		if contract.Name != want.Name || contract.Version != want.Version || contract.Fingerprint != want.Fingerprint {
			t.Errorf("%s = {%s v%d %s}, Pi has {%s v%d %s}", name, contract.Name, contract.Version, contract.Fingerprint, want.Name, want.Version, want.Fingerprint)
		}
	}
}

func TestWorkspacePath(t *testing.T) {
	for path, ok := range map[string]bool{
		"": true, "src/a.ts": true, ".github/x": true, "a/b": true,
		"/etc": false, "~/x": false, "a//b": false, "a/./b": false, "../x": false, "a/..": false, "a\\b": false, "a\x00": false,
		".git": false, ".git/config": false, ".gitignore": true,
	} {
		if _, got := WorkspacePath(path); got != ok {
			t.Errorf("WorkspacePath(%q) = %v, want %v", path, got, ok)
		}
	}
}

func TestWorkspaceGlobsAndSearchText(t *testing.T) {
	for value, want := range map[string][]string{
		"":            {},
		"*.ts, src/":  {"**/*.ts", "**/src"},
		"*.{ts,tsx}":  {"**/*.ts", "**/*.tsx"},
		"./src/*.ts":  {"src/*.ts"},
		"/dist":       {"dist"},
		"**/a, a/**":  {"**/a", "**/a/**"},
		"{a,b}/{c,d}": {"**/a/c", "**/a/d", "**/b/c", "**/b/d"},
		"*.ts,*.ts":   {"**/*.ts"},
		"{a,b},c":     {"**/a", "**/b", "**/c"},
	} {
		got, ok := WorkspaceGlobs(value)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("WorkspaceGlobs(%q) = %v, %v; want %v", value, got, ok, want)
		}
	}
	for _, value := range []string{"{a", "a}", "../x", "a\\b", "a\tb", "{a,b}{c,d}{e,f}{g,h}{i,j}{k,l}"} {
		if got, ok := WorkspaceGlobs(value); ok {
			t.Errorf("WorkspaceGlobs(%q) = %v, want invalid", value, got)
		}
	}
	for value, ok := range map[string]bool{"x": true, "": false, "  ": false, "a\nb": false, "a\x00": false} {
		if _, got := WorkspaceSearchText(value); got != ok {
			t.Errorf("WorkspaceSearchText(%q) = %v", value, got)
		}
	}
}
