package codingagent

import (
	"path/filepath"
	"testing"
)

// slash-commands.ts:4: SlashCommandSource is "extension" | "prompt" | "skill". Prompt templates and skills list under their source in pi.getCommands.
func TestSlashCommandCatalogListsPromptAndSkillSources(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	catalog := SlashCommandCatalog{
		CWD: cwd, AgentDir: agentDir, SourceInfo: map[string]ResourceSourceInfo{},
		PromptTemplates: []PromptTemplate{{Name: "fix", Description: "Fix it", FilePath: filepath.Join(agentDir, "prompts", "fix.md")}},
		Skills:          []*SkillDef{{Name: "lint", Description: "Lint code", FilePath: filepath.Join(agentDir, "skills", "lint", "SKILL.md")}},
	}
	var got []SlashCommandSource
	names := map[SlashCommandSource]string{}
	for _, command := range catalog.Commands() {
		source := SlashCommandSource(command.Source)
		got = append(got, source)
		names[source] = command.Name
	}
	if len(got) != 2 || names["prompt"] != "fix" || names["skill"] != "skill:lint" || SlashSourceSkill != "skill" {
		t.Fatalf("sources = %v, names = %v", got, names)
	}
}

// resource-loader.ts findSourceInfoForPath: a prompt template the loader read carries a synthetic temporary sourceInfo, and get_commands reports the metadata the resolver recorded for its path (project settings entry: local, project, top-level, no baseDir).
func TestSlashCommandCatalogPromptUsesRecordedSourceInfoOverItsOwn(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	path := filepath.Join(cwd, ".pig", "extra", "a.md")
	synthetic := CreateSyntheticSourceInfo(path, SyntheticSourceInfoOptions{Source: "local", BaseDir: filepath.Dir(path)})
	catalog := SlashCommandCatalog{
		CWD: cwd, AgentDir: agentDir,
		SourceInfo:      map[string]ResourceSourceInfo{path: {Path: path, Scope: "project", Origin: "top-level", Source: "local"}},
		PromptTemplates: []PromptTemplate{{Name: "a", FilePath: path, SourceInfo: synthetic}},
	}
	got := catalog.Commands()[0].SourceInfo
	want := PiSourceInfo{Path: path, Source: "local", Scope: "project", Origin: "top-level"}
	if got != want {
		t.Fatalf("sourceInfo = %+v, want %+v", got, want)
	}
	// Without recorded metadata the template keeps its own sourceInfo.
	catalog.SourceInfo = map[string]ResourceSourceInfo{}
	if got := catalog.Commands()[0].SourceInfo; got != synthetic {
		t.Fatalf("unrecorded sourceInfo = %+v, want %+v", got, synthetic)
	}
}
