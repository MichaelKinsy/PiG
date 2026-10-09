package tools

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

func TestToolSystemPromptContributionsPort(t *testing.T) {
	// .upstream/v0.87.1/packages/coding-agent/test/tool-system-prompt-contributions.test.ts:26 (all eight rows at :15-22).
	// Go keeps prompt snippets outside the provider schema. Exercise the same
	// schema/guideline plus snippet composition that builds Session prompts.
	cases := []struct {
		name, snippet string
		guidelines    []string
	}{
		{"read", "Read file contents", []string{"Use read to examine files instead of cat or sed."}},
		{"bash", "Execute bash commands (ls, grep, find, etc.)", []string{"You can inspect PI_* environment variables for current model and session details."}},
		{"powershell", "Execute PowerShell commands", []string{"You can inspect PI_* environment variables for current model and session details."}},
		{"edit", "Make precise file edits with exact text replacement, including multiple disjoint edits in one call", []string{
			"Use edit for precise changes (edits[].oldText must match exactly)",
			"When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
			"Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
			"Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
		}},
		{"write", "Create or overwrite files", []string{"Use write only for new files or complete rewrites."}},
		{"grep", "Search file contents for patterns (respects .gitignore)", nil},
		{"find", "Find files by glob pattern (respects .gitignore)", nil},
		{"ls", "List directory contents", nil},
	}
	all := make(map[string]ai.ToolSchema)
	for _, tool := range CreateAllTools("/workspace", nil) {
		all[tool.Name()] = tool.Schema()
	}
	for _, tc := range cases {
		t.Run("keeps the "+tc.name+" tool definition aligned with its contribution", func(t *testing.T) {
			definition, ok := all[tc.name]
			if !ok {
				t.Fatal("missing definition")
			}
			hints := prompts.DefaultToolSnippets()
			if hints[tc.name] != tc.snippet || !slices.Equal(definition.PromptGuidelines, tc.guidelines) || !slices.Equal(DefaultToolGuidelines()[tc.name], tc.guidelines) {
				t.Fatalf("snippet %q, guidelines %q; want %q, %q", hints[tc.name], definition.PromptGuidelines, tc.snippet, tc.guidelines)
			}
			sections := prompts.BuildSystemPromptSections(prompts.Options{Cwd: "/workspace", Tools: []string{tc.name}, ToolHints: hints, ToolGuidelines: DefaultToolGuidelines()})
			prompt := ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Sections: sections}})
			for _, part := range append([]string{"- " + tc.name + ": " + tc.snippet}, tc.guidelines...) {
				if !strings.Contains(prompt, part) {
					t.Fatalf("prompt lacks %q: %s", part, prompt)
				}
			}
		})
	}
}
