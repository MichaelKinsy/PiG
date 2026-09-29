package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

type skillMetadataFixture struct {
	cwd, agent string
	settings   *codingagent.SettingsManager
}

func newSkillMetadataFixture(t *testing.T) skillMetadataFixture {
	t.Helper()
	cwd, agent := t.TempDir(), t.TempDir()
	t.Setenv("HOME", cwd)
	t.Setenv("USERPROFILE", cwd)
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_OFFLINE", "1")
	return skillMetadataFixture{cwd, agent, codingagent.NewSettingsManager(cwd, agent)}
}

func writeSkillMetadataFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireSkillMetadata(t *testing.T, infos map[string]codingagent.ResourceSourceInfo, path, source, scope, base string) {
	t.Helper()
	info, ok := infos[path]
	if !ok || info.Path != path || info.ResourceType != "skills" || !info.Enabled || info.Source != source || info.Scope != scope || info.BaseDir != base {
		t.Fatalf("metadata for %s = %+v (present %t), want %s/%s/%s; all=%+v", path, info, ok, source, scope, base, infos)
	}
	if _, directoryEntry := infos[filepath.Dir(path)]; directoryEntry {
		t.Fatalf("skill directory and file both published: %+v", infos)
	}
}

// Copies gate-close-00's exact metadata cases from packages/coding-agent/test/package-manager.test.ts:301,314,328,353.
func TestSkillResourceMetadataUpstream(t *testing.T) {
	for _, tc := range []struct{ name, base, scope, skill, description string }{
		{"should use the agent dir as baseDir for user .pi/agent skills", "agent", "user", "user-pi", "user pi"},
		{"should use the project .pi dir as baseDir for project .pi skills", "project", "project", "project-pi", "project pi"},
		{"should use ~/.agents as baseDir for user .agents skills", "agents", "user", "user-agents", "user agents"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSkillMetadataFixture(t)
			base := f.agent
			if tc.base == "project" {
				base = filepath.Join(f.cwd, codingagent.CONFIG_DIR_NAME)
			}
			if tc.base == "agents" {
				base = filepath.Join(f.cwd, ".agents")
			}
			path := filepath.Join(base, "skills", tc.skill, "SKILL.md")
			writeSkillMetadataFile(t, path, "---\nname: "+tc.skill+"\ndescription: "+tc.description+"\n---\n")
			requireSkillMetadata(t, resourceSourceInfoProvider(f.cwd, f.agent, f.settings, CLIFlags{})(), path, "auto", tc.scope, base)
		})
	}
	t.Run("should use each project .agents dir as baseDir for project .agents skills", func(t *testing.T) {
		f := newSkillMetadataFixture(t)
		repo := filepath.Join(f.cwd, "repo")
		f.cwd = filepath.Join(repo, "packages", "feature")
		if err := os.MkdirAll(f.cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		bases := []string{filepath.Join(repo, ".agents"), filepath.Join(repo, "packages", ".agents")}
		for i, name := range []string{"repo", "package"} {
			writeSkillMetadataFile(t, filepath.Join(bases[i], "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: "+name+"\n---\n")
		}
		infos := resourceSourceInfoProvider(f.cwd, f.agent, f.settings, CLIFlags{})()
		for i, name := range []string{"repo", "package"} {
			requireSkillMetadata(t, infos, filepath.Join(bases[i], "skills", name, "SKILL.md"), "auto", "project", bases[i])
		}
	})
}

func TestSkillMetadataUsesFilesWithoutChangingConfigBundlePaths(t *testing.T) {
	f := newSkillMetadataFixture(t)
	dir := filepath.Join(f.agent, "skills", "my-skill")
	file := filepath.Join(dir, "SKILL.md")
	writeSkillMetadataFile(t, file, "---\nname: test-skill\ndescription: A test skill\n---\nContent")
	if err := f.settings.SetSkillPaths([]string{"skills"}); err != nil {
		t.Fatal(err)
	}
	// Original package-manager.test.ts:125 input resolves the entry file, not its bundle directory.
	infos := resourceSourceInfoProvider(f.cwd, f.agent, f.settings, CLIFlags{})()
	requireSkillMetadata(t, infos, file, "local", "user", "")
	relative, err := filepath.Rel(f.agent, infos[file].Path)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("SKILL_METADATA %s\n", filepath.ToSlash(relative))
	items, err := collectConfigResourceItems(f.cwd, f.agent, f.settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ResourceType == tui.ResourceSkills {
			if item.Path != dir {
				t.Fatalf("config bundle path changed: %+v", item)
			}
			return
		}
	}
	t.Fatal("config skill entry missing")
}

func TestExplicitSkillFileMetadataWinsOverAutomaticBundle(t *testing.T) {
	f := newSkillMetadataFixture(t)
	file := filepath.Join(f.agent, "skills", "my-skill", "SKILL.md")
	writeSkillMetadataFile(t, file, "---\nname: test-skill\ndescription: A test skill\n---\nContent")
	if err := f.settings.SetSkillPaths([]string{file}); err != nil {
		t.Fatal(err)
	}
	requireSkillMetadata(t, resourceSourceInfoProvider(f.cwd, f.agent, f.settings, CLIFlags{})(), file, "local", "user", "")
}

func TestSkillMetadataProjection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "example")
	file := filepath.Join(dir, "SKILL.md")
	writeSkillMetadataFile(t, file, "---\nname: example\ndescription: Example\n---\n")
	for _, path := range []string{dir, file} {
		info := sourceInfoFromResourceItem(tui.ResourceItem{Path: path, ResourceType: tui.ResourceSkills, Enabled: true, Scope: "user", Origin: "package", Source: "npm:example", BaseDir: filepath.Dir(dir)})
		if info.Path != file || info.BaseDir != filepath.Dir(dir) || info.Source != "npm:example" {
			t.Fatalf("projected skill = %+v", info)
		}
	}
	standalone := filepath.Join(t.TempDir(), "standalone.md")
	writeSkillMetadataFile(t, standalone, "---\ndescription: Standalone\n---\n")
	info := sourceInfoFromResourceItem(tui.ResourceItem{Path: standalone, ResourceType: tui.ResourceSkills})
	if info.Path != standalone {
		t.Fatalf("standalone skill = %+v", info)
	}
}
