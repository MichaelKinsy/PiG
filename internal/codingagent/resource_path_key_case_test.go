package codingagent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/resolvepath"
)

// Ports packages/coding-agent/test/source-info.test.ts (Pi 1be312b4, #10488): file paths fold case on Windows; builtin and inline identifiers keep theirs.
func TestResourcePathKeyFoldsFilePathCasingOnWindowsButNotSyntheticIdentifiers(t *testing.T) {
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	if ResourcePathKey(`C:\Users\Me\ext.ts`) != ResourcePathKey(`c:\users\me\ext.ts`) {
		t.Error("one file in two casings has two keys")
	}
	if got := ResourcePathKey("builtin:MCP"); got != "builtin:MCP" {
		t.Errorf("key of builtin:MCP = %q, want it unchanged", got)
	}
	if ResourcePathKey("builtin:MCP") == ResourcePathKey("builtin:mcp") {
		t.Error("builtin:MCP and builtin:mcp share a key")
	}
	if got := ResourcePathKey("<inline:Name>"); got != "<inline:Name>" {
		t.Errorf("key of <inline:Name> = %q, want it unchanged", got)
	}
}

// skills.ts loadSkills classifies an explicit path under the user or project skills directory (getSource with isPathWithin): on Windows the directory the
// agent directory names may differ from the path in casing only.
func TestLoadSkillsClassifiesAnExplicitPathUnderTheUserSkillsDirectoryRegardlessOfCasingOnWindows(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "agent", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: demo\ndescription: a demo\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resolvepath.OverridePlatform("windows"))
	result, err := LoadSkills(LoadSkillsOptions{CWD: filepath.Join(root, "project"), AgentDir: filepath.Join(root, "AGENT"), SkillPaths: []string{skillDir}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %+v, diagnostics = %+v", result.Skills, result.Diagnostics)
	}
	if got := result.Skills[0].SourceInfo.Scope; got != "user" {
		t.Fatalf("scope = %q, want the user skills directory to claim the path", got)
	}
}
