package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func writeResourceTestFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for path, content := range files {
		path = filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:370-378,417-438,486-495 using the context and system-input paths shared by startup and reload.
func TestUpstreamResourceLoaderContextOptions(t *testing.T) {
	for _, tc := range []struct {
		name, system, appendix string
		files                  map[string]string
		noContext              bool
		wantContext            bool
	}{
		{name: "discover AGENTS.md", files: map[string]string{"project/AGENTS.md": "# Project Guidelines\n\nBe helpful."}, wantContext: true},
		{name: "noContextFiles", noContext: true, files: map[string]string{"project/AGENTS.override.md": "# Override Guidelines\n\nBe helpful.", "project/AGENTS.md": "# Project Guidelines\n\nBe helpful.", "project/CLAUDE.md": "# Claude Guidelines\n\nBe helpful."}},
		{name: "discover SYSTEM.md", system: "You are a helpful assistant.", files: map[string]string{"project/.pig/SYSTEM.md": "You are a helpful assistant."}},
		{name: "discover APPEND_SYSTEM.md", appendix: "Additional instructions.", files: map[string]string{"project/.pig/APPEND_SYSTEM.md": "Additional instructions."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("PIG_HOME", filepath.Join(root, "config"))
			writeResourceTestFiles(t, root, tc.files)
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			files := loadContextFiles(cwd, agentDir, tc.noContext)
			if tc.wantContext {
				found := false
				for _, file := range files {
					found = found || strings.Contains(file.Path, "AGENTS.md")
				}
				if !found {
					t.Fatalf("context files = %#v", files)
				}
			} else if len(files) != 0 {
				t.Fatalf("unexpected context files: %#v", files)
			}
			inputs := resolvePromptInputs(cwd, agentDir, CLIFlags{}, true)
			if inputs.custom != tc.system || inputs.append != tc.appendix {
				t.Fatalf("prompt inputs = %#v", inputs)
			}
		})
	}
}

// Ports packages/coding-agent/test/resource-loader.test.ts:440-484. Project context remains visible without trust, but executable/configuration resources do not load.
func TestUpstreamResourceLoaderUntrustedProject(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "config"))
	writeResourceTestFiles(t, root, map[string]string{
		"project/.pig/SYSTEM.md":                     "Project system prompt.",
		"agent/SYSTEM.md":                            "Global system prompt.",
		"agent/AGENTS.md":                            "Global instructions",
		"project/AGENTS.md":                          "Project instructions",
		"project/.pig/extensions/project.ts":         `throw new Error("should not load");`,
		"project/.pig/skills/project-skill/SKILL.md": "---\nname: project-skill\ndescription: Project skill\n---\nProject skill content",
		"project/.pig/prompts/project.md":            "Project prompt",
	})
	theme, err := os.ReadFile("../../.upstream/current/packages/coding-agent/src/modes/interactive/theme/dark.json")
	if err != nil {
		t.Fatal(err)
	}
	writeResourceTestFiles(t, root, map[string]string{"project/.pig/themes/project.json": strings.Replace(string(theme), `"name": "dark"`, `"name": "project-theme"`, 1)})
	cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
	sm := codingagent.NewSettingsManagerWithProjectTrust(cwd, agentDir, false)
	scopes := trustedAmbientScopes(nil)
	configs := collectExtensionConfigs(cwd, agentDir, sm, CLIFlags{}, scopes)
	host := subprocess.NewHostWithConfigRoot(cwd, filepath.Join(root, "host"))
	t.Cleanup(func() { host.Shutdown("test done") })
	extensions, errs := host.LoadAll(t.Context(), configs)
	if len(extensions) != 0 || len(errs) != 0 {
		t.Fatalf("untrusted extensions = %#v errors=%v", extensions, errs)
	}
	inputs := resolvePromptInputs(cwd, agentDir, CLIFlags{}, false)
	if inputs.custom != "Global system prompt." {
		t.Fatalf("untrusted system prompt = %q", inputs.custom)
	}
	files := loadContextFiles(cwd, agentDir, false)
	for _, expected := range []string{filepath.Join(agentDir, "AGENTS.md"), filepath.Join(cwd, "AGENTS.md")} {
		found := false
		for _, file := range files {
			found = found || file.Path == expected
		}
		if !found {
			t.Errorf("missing context %q: %#v", expected, files)
		}
	}
	skills, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: cwd, AgentDir: agentDir, SkillPaths: collectSkillInputs(cwd, agentDir, sm, CLIFlags{}, scopes)})
	if err != nil || len(skills.Skills) != 0 {
		t.Fatalf("untrusted skills = %#v error=%v", skills, err)
	}
	prompts := codingagent.LoadPromptTemplates("", "", collectPromptPaths(cwd, agentDir, sm, CLIFlags{}, false)...)
	if len(prompts.Templates) != 0 {
		t.Fatalf("untrusted prompts = %#v", prompts)
	}
	if paths := collectThemePaths(cwd, agentDir, sm, CLIFlags{}, false); len(paths) != 0 {
		t.Fatalf("untrusted themes = %q", paths)
	}
}
