package codingagent

import (
	"path/filepath"
	"testing"
)

// Pi resource-loader.ts:685-691 overlays resolver and extension provenance on the loaded skill; it leaves the original loader record unchanged.
func TestPromptSkillSourceProjection(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	for _, tc := range []struct {
		name     string
		path     string
		metadata *ResourceSourceInfo
		want     PiSourceInfo
	}{
		{name: "user", path: filepath.Join(agentDir, "skills", "review", "SKILL.md"), want: PiSourceInfo{Source: "local", Scope: "user", Origin: "top-level", BaseDir: filepath.Join(agentDir, "skills")}},
		{name: "project", path: filepath.Join(ProjectConfigDir(cwd), "skills", "review", "SKILL.md"), want: PiSourceInfo{Source: "local", Scope: "project", Origin: "top-level", BaseDir: filepath.Join(ProjectConfigDir(cwd), "skills")}},
		{name: "cli-extension", path: filepath.Join(cwd, "explicit", "SKILL.md"), metadata: &ResourceSourceInfo{Source: "cli", Scope: "temporary", Origin: "top-level"}, want: PiSourceInfo{Source: "cli", Scope: "temporary", Origin: "top-level"}},
		{name: "package", path: filepath.Join(cwd, "package", "SKILL.md"), metadata: &ResourceSourceInfo{Source: "npm:review", Scope: "user", Origin: "package", BaseDir: cwd}, want: PiSourceInfo{Source: "npm:review", Scope: "user", Origin: "package", BaseDir: cwd}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := SlashCommandCatalog{CWD: cwd, AgentDir: agentDir, SourceInfo: map[string]ResourceSourceInfo{}}
			if tc.metadata != nil {
				catalog.SourceInfo[filepath.Dir(tc.path)] = *tc.metadata
			}
			original := &SkillDef{FilePath: tc.path, Name: "review"}
			got := catalog.WithSkillSources([]*SkillDef{original})
			want := tc.want
			want.Path = tc.path
			if len(got) != 1 || got[0].SourceInfo != want {
				t.Fatalf("projected sources = %+v, want %+v", got, want)
			}
			if original.SourceInfo != (PiSourceInfo{}) {
				t.Fatalf("projection mutated original: %+v", original.SourceInfo)
			}
		})
	}
}

// Pi core/slash-commands.ts SlashCommandSource: extension, prompt and skill are exactly these literals, and the catalog reports skills with the skill source.
func TestSlashCommandSourceLiteralsMatchUpstream(t *testing.T) {
	if SlashSourceExtension != "extension" || SlashSourcePrompt != "prompt" || SlashSourceSkill != "skill" {
		t.Fatalf("sources %q %q %q", SlashSourceExtension, SlashSourcePrompt, SlashSourceSkill)
	}
	catalog := SlashCommandCatalog{
		CWD: "/work", AgentDir: "/agent", SourceInfo: map[string]ResourceSourceInfo{},
		PromptTemplates: []PromptTemplate{{Name: "greet", Description: "Greeting", FilePath: "/agent/prompts/greet.md"}},
		Skills:          []*SkillDef{{Name: "demo", Description: "A demo skill", FilePath: "/agent/skills/demo/SKILL.md"}},
	}
	sources := map[string]string{}
	for _, command := range catalog.Commands() {
		sources[command.Name] = command.Source
	}
	if sources["greet"] != string(SlashSourcePrompt) || sources["skill:demo"] != string(SlashSourceSkill) {
		t.Fatalf("catalog sources %v", sources)
	}
}

// Pi resource-loader.ts:880-882 gives a prompt template the source info recorded for its path (an extension's resources_discover or the
// package resolver) before the one it was loaded with, so get_commands reports the extension, not the loader's default "local".
func TestPromptCommandSourceInfoPrefersRecordedProvenance(t *testing.T) {
	cwd, agentDir := t.TempDir(), t.TempDir()
	path := filepath.Join(cwd, "ext", "prompts", "discovered.md")
	loaded := PiSourceInfo{Path: path, Source: "local", Scope: "temporary", Origin: "top-level", BaseDir: filepath.Join(cwd, "ext", "prompts")}
	catalog := SlashCommandCatalog{
		CWD: cwd, AgentDir: agentDir,
		PromptTemplates: []PromptTemplate{{Name: "discovered", FilePath: path, SourceInfo: loaded}},
		SourceInfo: map[string]ResourceSourceInfo{
			filepath.Join(cwd, "ext", "prompts"): {Source: "extension:discover", Scope: "temporary", Origin: "top-level", BaseDir: filepath.Join(cwd, "ext")},
		},
	}
	got := catalog.Commands()[0].SourceInfo
	want := PiSourceInfo{Path: path, Source: "extension:discover", Scope: "temporary", Origin: "top-level", BaseDir: filepath.Join(cwd, "ext")}
	if got != want {
		t.Fatalf("prompt sourceInfo = %+v, want %+v", got, want)
	}
	catalog.SourceInfo = nil
	if got := catalog.Commands()[0].SourceInfo; got != loaded {
		t.Fatalf("without recorded provenance the loaded info stays: %+v, want %+v", got, loaded)
	}
}
