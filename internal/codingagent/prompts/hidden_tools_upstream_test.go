package prompts

import (
	"os"
	"strings"
	"testing"
)

// Ports the "hidden tools" describe of packages/coding-agent/test/system-prompt.test.ts (1.0.4, #10343): hidden tools
// are reachable only through another tool, so the tool list and rules leave them out and the skills hint names no hidden
// reader.
func TestUpstreamSystemPromptHiddenTools(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	skill := Skill{Name: "test-skill", Description: "A test skill.", Path: "/skills/test-skill/SKILL.md"}
	build := func(hidden ...string) string {
		return BuildDefaultPrompt(Options{
			Cwd: cwd, Tools: []string{"read", "bash", "run"}, HiddenTools: hidden,
			ToolHints:      map[string]string{"read": "Read files", "bash": "Run commands", "run": "Run a task"},
			ToolGuidelines: map[string][]string{"read": {"Use read for files."}, "run": {"Prefer run."}},
			Skills:         []Skill{skill},
		})
	}

	t.Run("leaves hidden tools out of the tool list and rules", func(t *testing.T) {
		prompt := build("read", "bash")
		if !strings.Contains(prompt, "<tools>\n- run: Run a task\n") {
			t.Errorf("tool list: %s", prompt)
		}
		for _, absent := range []string{"- read: ", "Use read for files.", "Use bash for file operations"} {
			if strings.Contains(prompt, absent) {
				t.Errorf("prompt contains %q:\n%s", absent, prompt)
			}
		}
		if !strings.Contains(prompt, "- Prefer run.") {
			t.Errorf("rules lack the visible tool's guideline:\n%s", prompt)
		}
	})

	t.Run("keeps skills without naming a hidden reader", func(t *testing.T) {
		for _, tc := range []struct {
			hidden []string
			want   string
		}{
			{[]string{"read", "bash"}, "\nLoad a skill's file when the task matches its description."},
			{[]string{"read"}, "Use bash to load a skill's file"},
			{nil, "Use the read tool to load a skill's file"},
		} {
			if prompt := build(tc.hidden...); !strings.Contains(prompt, tc.want) {
				t.Errorf("hidden %v: prompt lacks %q:\n%s", tc.hidden, tc.want, prompt)
			}
		}
	})
}
