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
			original := &SkillDef{Path: tc.path, Name: "review"}
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
