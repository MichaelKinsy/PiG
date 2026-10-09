//go:build !pig_strip_docs

package prompts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Cases 8-14 of packages/coding-agent/test/system-prompt.test.ts (111-209). Keep their original inputs and strengthen substring/count checks to complete emitted-section equality. D22 owns only the PiG product and documentation-location literals.
func TestUpstreamSystemPromptSecondHalfExactSections(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	docsRoot := filepath.Join(home, "docs")
	docs := "<docs>\nPiG documentation (read only when the user asks about pig itself, its SDK, extensions, themes, skills, or TUI):\n" +
		"- Main documentation: " + filepath.Join(docsRoot, "README.md") + "\n" +
		"- Additional docs: " + docsRoot + "\n" +
		"- Examples: https://github.com/MichaelKinsy/PiG/tree/main/examples (extensions, custom tools, SDK)\n" +
		"- When reading pig docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n" +
		"- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pig packages (docs/packages.md), environment variables (docs/environment-variables.md), MCP servers (docs/mcp.md), codemode scripts and non-LLM models such as classifiers and image models (docs/codemode.md)\n" +
		"- When working on pig topics, read the docs and examples, and follow .md cross-references before implementing\n" +
		"- Always read pig .md files completely and follow links to related docs (e.g., tui.md for TUI API details)\n</docs>"
	const toolsTail = "\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.\n</tools>"
	const rulesTail = "\n- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>"
	const skills = `<skills>
The following skills provide specialized instructions for specific tasks.
Use bash to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>test-skill</name>
    <description>A test skill.</description>
    <location>/skills/test-skill/SKILL.md</location>
  </skill>
</available_skills>
</skills>`
	skill := Skill{Name: "test-skill", Description: "A test skill.", Path: "/skills/test-skill/SKILL.md"}
	for _, tc := range []struct {
		name, section, want string
		opts                Options
	}{
		{"111 docs absolute bases", "docs", docs, Options{Cwd: cwd}},
		{"126 custom tool snippet", "tools", "<tools>\n- dynamic_tool: Run dynamic test behavior" + toolsTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, ToolHints: map[string]string{"dynamic_tool": "Run dynamic test behavior"}}},
		{"140 omitted custom snippet", "tools", "<tools>\n(none)" + toolsTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}}},
		{"153 appended guideline", "rules", "<rules>\n- Use dynamic_tool for project summaries." + rulesTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for project summaries."}}},
		{"165 trimmed unique guideline", "rules", "<rules>\n- Use dynamic_tool for summaries." + rulesTail, Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for summaries.", "  Use dynamic_tool for summaries.  ", "   "}}},
		{"179 bash skills default prompt", "skills", skills, Options{Cwd: cwd, Tools: []string{"bash"}, Skills: []Skill{skill}}},
		{"179 bash skills custom prompt", "skills", skills, Options{Cwd: cwd, CustomPrompt: "Custom system prompt", AppendMode: "replace", Tools: []string{"bash"}, Skills: []Skill{skill}}},
		{"197 no skill reader", "skills", "", Options{Cwd: cwd, Tools: []string{"write"}, Skills: []Skill{skill}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := BuildDefaultPrompt(tc.opts)
			start, end := "<"+tc.section+">", "</"+tc.section+">"
			_, rest, found := strings.Cut(prompt, start)
			if tc.want == "" {
				if found {
					t.Fatalf("unexpected %s section in %q", tc.section, prompt)
				}
				return
			}
			content, _, closed := strings.Cut(rest, end)
			if !found || !closed {
				t.Fatalf("missing complete %s section in %q", tc.section, prompt)
			}
			if got := start + content + end; got != tc.want {
				t.Errorf("%s section:\ngot  %q\nwant %q", tc.section, got, tc.want)
			}
		})
	}
}
