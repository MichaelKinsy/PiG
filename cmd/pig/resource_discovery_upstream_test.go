package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The CLI's selected paths feed the same skills/prompt readers on startup and reload.
// Ports packages/coding-agent/test/resource-loader.test.ts:43,62,83,102,326,786,805.
func TestUpstreamResourceLoaderDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name          string
		files         map[string]string
		flags         CLIFlags
		skill, prompt string
		invalidPrompt bool
	}{
		// upstream: packages/coding-agent/test/resource-loader.test.ts:43
		{name: "should discover skills from agentDir", files: map[string]string{"agent/skills/test-skill.md": "---\nname: test-skill\ndescription: A test skill\n---\nSkill content here."}, skill: "test-skill"},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:62
		{name: "should ignore extra markdown files in auto-discovered skill dirs", files: map[string]string{"agent/skills/pi-skills/browser-tools/SKILL.md": "---\nname: browser-tools\ndescription: Browser tools\n---\nSkill content here.", "agent/skills/pi-skills/browser-tools/EFFICIENCY.md": "No frontmatter here"}, skill: "browser-tools"},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:83
		{name: "should discover prompts from agentDir", files: map[string]string{"agent/prompts/test-prompt.md": "---\ndescription: A test prompt\n---\nPrompt content."}, prompt: "test-prompt"},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:102
		{name: "should report invalid prompt frontmatter while loading valid siblings", files: map[string]string{"agent/prompts/invalid.md": "---\ndescription: Broken: unquoted colon\n---\nDo something.\n", "agent/prompts/valid.md": "Valid prompt content."}, prompt: "valid", invalidPrompt: true},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:326
		{name: "should honor overrides for auto-discovered resources", files: map[string]string{
			"agent/settings.json":              `{"extensions":["-extensions/disabled.ts"],"skills":["-skills/skip-skill"],"prompts":["-prompts/skip.md"],"themes":["-themes/skip.json"]}`,
			"agent/extensions/disabled.ts":     "export default function() {}",
			"agent/skills/skip-skill/SKILL.md": "---\nname: skip-skill\ndescription: Skip me\n---\nContent",
			"agent/prompts/skip.md":            "Skip prompt", "agent/themes/skip.json": "{}",
		}},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:786
		{name: "should skip skill discovery when noSkills is true", files: map[string]string{"agent/skills/test-skill.md": "---\nname: test-skill\ndescription: A test skill\n---\nContent"}, flags: CLIFlags{NoSkills: true}},
		// upstream: packages/coding-agent/test/resource-loader.test.ts:805
		{name: "should still load additional skill paths when noSkills is true", files: map[string]string{"custom-skills/custom.md": "---\nname: custom\ndescription: Custom skill\n---\nContent"}, flags: CLIFlags{NoSkills: true, Skills: []string{"../custom-skills"}}, skill: "custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			t.Setenv("USERPROFILE", root)
			t.Setenv("PIG_HOME", filepath.Join(root, "config"))
			cwd, agentDir := filepath.Join(root, "project"), filepath.Join(root, "agent")
			for _, dir := range []string{cwd, agentDir} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			writeResourceTestFiles(t, root, tc.files)
			sm := codingagent.NewSettingsManager(cwd, agentDir)
			skills, err := codingagent.LoadSkills(codingagent.LoadSkillsOptions{CWD: cwd, AgentDir: agentDir, SkillPaths: collectSkillInputs(cwd, agentDir, sm, tc.flags, nil)})
			if err != nil {
				t.Fatal(err)
			}
			if tc.skill == "" {
				if len(skills.Skills) != 0 {
					t.Fatalf("skills=%#v; want empty", skills.Skills)
				}
			} else if !slices.ContainsFunc(skills.Skills, func(s *codingagent.SkillDef) bool { return s.Name == tc.skill }) {
				t.Fatalf("skills=%#v; missing %q", skills.Skills, tc.skill)
			}
			for _, diagnostic := range skills.Diagnostics {
				if strings.HasSuffix(diagnostic.Path, "EFFICIENCY.md") {
					t.Errorf("ordinary skill documentation produced diagnostic: %+v", diagnostic)
				}
			}
			prompts := codingagent.LoadPromptTemplates("", "", collectPromptPaths(cwd, agentDir, sm, tc.flags, true)...)
			var names []string
			for _, prompt := range prompts.Templates {
				names = append(names, prompt.Name)
			}
			var want []string
			if tc.prompt != "" {
				want = []string{tc.prompt}
			}
			if !slices.Equal(names, want) {
				t.Fatalf("prompt names=%q; want %q", names, want)
			}
			if tc.invalidPrompt {
				if len(prompts.Diagnostics) != 1 {
					t.Fatalf("diagnostics=%#v; want one invalid prompt warning", prompts.Diagnostics)
				}
				d := prompts.Diagnostics[0]
				if d.Type != "warning" || d.Path != filepath.Join(agentDir, "prompts", "invalid.md") || !strings.Contains(d.Message, "line 1, column 14") {
					t.Fatalf("invalid prompt diagnostic=%+v", d)
				}
			}
			if got := collectExtensionConfigs(cwd, agentDir, sm, tc.flags, nil); len(got) != 0 {
				t.Fatalf("extension configs=%#v; disabled extension should not be selected", got)
			}
			if got := collectThemePaths(cwd, agentDir, sm, tc.flags, true); len(got) != 0 {
				t.Fatalf("theme paths=%q; disabled theme should not be selected", got)
			}
		})
	}
}
