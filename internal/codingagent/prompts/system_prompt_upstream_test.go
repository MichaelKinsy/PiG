//go:build !pig_strip_docs

package prompts

// pi: packages/coding-agent/src/core/system-prompt.ts

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func BenchmarkBuildSystemPrompt(b *testing.B) {
	for _, tc := range []struct {
		name string
		opts Options
	}{
		{"default", Options{Cwd: "/work", ToolHints: DefaultToolSnippets()}},
		{"project_context_64KiB", Options{Cwd: "/work", ToolHints: DefaultToolSnippets(), ContextFiles: []struct{ Path, Content string }{{"/work/AGENTS.md", strings.Repeat("instruction\n", 65536/len("instruction\n"))}}}},
		{"forced", Options{Cwd: "/work", ForceSystemPrompt: new("exact")}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_ = BuildDefaultPrompt(tc.opts)
			}
		})
	}
}

func TestSystemPromptParityProbe(t *testing.T) {
	toolSection := func(opts Options) string {
		for _, section := range BuildSystemPromptSections(opts) {
			if section.Name == "tools" && section.Value != nil {
				return *section.Value
			}
		}
		t.Fatal("missing tools section")
		return ""
	}
	snippets := map[string]string{"read": "Read file contents", "bash": "Execute bash commands", "edit": "Make surgical edits", "write": "Create or overwrite files"}
	values := []string{
		toolSection(Options{Cwd: "/tmp", ToolHints: snippets}),
		toolSection(Options{Cwd: "/tmp", Tools: []string{}, ToolHints: snippets}),
		BuildDefaultPrompt(Options{Cwd: "/tmp", ForceSystemPrompt: new("exact")}),
		BuildDefaultPrompt(Options{Cwd: "/tmp", ForceSystemPrompt: new("")}),
	}
	fmt.Print("SYSTEM_PROMPT ")
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(values); err != nil {
		t.Fatal(err)
	}
}

func TestUpstreamSystemPrompt(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	skill := Skill{Name: "test-skill", Description: "A test skill.", Path: "/skills/test-skill/SKILL.md"}
	for _, tc := range []struct {
		name             string
		opts             Options
		contains, absent []string
		count            string
	}{
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:17
		{"shows (none) for empty tools list", Options{Cwd: cwd, Tools: []string{}}, []string{"<tools>\n(none)\n"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:28
		{"shows file paths guideline even with no tools", Options{Cwd: cwd, Tools: []string{}}, []string{"Show file paths clearly"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:59
		{"maps appended instructions and project context to stable sections", Options{CustomPrompt: "You are Exact.", AppendMode: "replace", AppendSystemPrompt: "Additional instructions.", ContextFiles: []struct{ Path, Content string }{{"/tmp/AGENTS.md", "Project instructions."}}, Tools: []string{}, Cwd: "/tmp"}, []string{"<addendum>\nAdditional instructions.\n</addendum>", "<project_context>\nProject-specific instructions and guidelines:\n\n<project_instructions path=\"/tmp/AGENTS.md\">", "<cwd>\n/tmp\n</cwd>"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:78
		{"includes all default tools when snippets are provided", Options{Cwd: cwd, ToolHints: map[string]string{"read": "Read file contents", "bash": "Execute bash commands", "edit": "Make surgical edits", "write": "Create or overwrite files"}}, []string{"- read:", "- bash:", "- edit:", "- write:"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:97 — powershell row.
		{"uses shell-specific guidance for powershell", Options{Cwd: cwd, Tools: []string{"powershell"}}, []string{"Use PowerShell for file operations"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:97 — bash and powershell row.
		{"uses shell-specific guidance for bash and powershell", Options{Cwd: cwd, Tools: []string{"bash", "powershell"}}, []string{"Use bash or PowerShell for file operations"}, nil, ""},
		// .upstream/v0.99.1/packages/coding-agent/test/system-prompt.test.ts:111-121 — D22 owns only the product/docs literals; 0.99.1 adds the MCP servers (docs/mcp.md) destination.
		{"instructs models to resolve pi docs and examples under absolute base paths", Options{Cwd: cwd}, []string{"- When reading pig docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory", "environment variables (docs/environment-variables.md), MCP servers (docs/mcp.md)"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:126
		{"includes custom tools in available tools section when promptSnippet is provided", Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, ToolHints: map[string]string{"dynamic_tool": "Run dynamic test behavior"}}, []string{"- dynamic_tool: Run dynamic test behavior"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:140
		{"omits custom tools from available tools section when promptSnippet is not provided", Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}}, nil, []string{"dynamic_tool"}, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:153
		{"appends promptGuidelines to default guidelines", Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for project summaries."}}, []string{"- Use dynamic_tool for project summaries."}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:165
		{"deduplicates and trims promptGuidelines", Options{Cwd: cwd, Tools: []string{"read", "dynamic_tool"}, PromptGuidelines: []string{"Use dynamic_tool for summaries.", "  Use dynamic_tool for summaries.  ", "   "}}, nil, nil, "- Use dynamic_tool for summaries."},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:179 — default prompt row.
		{"includes skills with only bash in the default prompt", Options{Cwd: cwd, Tools: []string{"bash"}, Skills: []Skill{skill}}, []string{"<skills>", "<available_skills>", "<name>test-skill</name>", "Use bash to load a skill's file"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:179 — custom prompt row.
		{"includes skills with only bash in the custom prompt", Options{Cwd: cwd, CustomPrompt: "Custom system prompt", AppendMode: "replace", Tools: []string{"bash"}, Skills: []Skill{skill}}, []string{"<skills>", "<available_skills>", "<name>test-skill</name>", "Use bash to load a skill's file"}, nil, ""},
		// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:197
		{"omits skills without read or bash", Options{Cwd: cwd, Tools: []string{"write"}, Skills: []Skill{skill}}, nil, []string{"<available_skills>"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prompt := BuildDefaultPrompt(tc.opts)
			for _, want := range tc.contains {
				if !strings.Contains(prompt, want) {
					t.Errorf("prompt lacks %q:\n%s", want, prompt)
				}
			}
			for _, unwanted := range tc.absent {
				if strings.Contains(prompt, unwanted) {
					t.Errorf("prompt contains %q:\n%s", unwanted, prompt)
				}
			}
			if tc.count != "" && strings.Count(prompt, tc.count) != 1 {
				t.Errorf("guideline count = %d, want one normalized unique input", strings.Count(prompt, tc.count))
			}
		})
	}
	// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:55
	t.Run("preserves an exact forced prompt without sections", func(t *testing.T) {
		// Empty and whitespace-bearing replacements additionally distinguish a present force option from an omitted one.
		for _, exact := range []string{"exact", "", "  exact\n\n"} {
			opts := Options{ForceSystemPrompt: new(exact), Cwd: "/tmp"}
			if got := BuildDefaultPrompt(opts); got != exact {
				t.Fatalf("forced prompt = %q, want %q", got, exact)
			}
			if state := BuildSystemPromptState(opts); state.Sections != nil {
				t.Fatalf("forced prompt has sections: %v", state.Sections)
			}
		}
	})
	// .upstream/v0.87.1/packages/coding-agent/test/system-prompt.test.ts:41
	t.Run("keeps the default and custom prompt prefixes exact", func(t *testing.T) {
		// D22 substitutes pig for pi in the default product preamble, not in custom text.
		if prompt := BuildDefaultPrompt(Options{Cwd: "/tmp", Tools: []string{}}); !strings.HasPrefix(prompt, "You are an expert coding assistant operating inside pig") {
			t.Fatal(prompt)
		}
		if prompt := BuildDefaultPrompt(Options{Cwd: "/tmp", Tools: []string{}, CustomPrompt: "You are Exact.", AppendMode: "replace"}); !strings.HasPrefix(prompt, "You are Exact.\n\n<cwd>") {
			t.Fatal(prompt)
		}
	})
}
