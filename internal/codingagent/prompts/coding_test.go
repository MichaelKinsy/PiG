package prompts

import (
	"path/filepath"
	"strings"
	"testing"
)

var piSnippets = map[string]string{
	"read":  "Read file contents",
	"bash":  "Execute bash commands (ls, grep, find, etc.)",
	"edit":  "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
	"write": "Create or overwrite files",
}

// The expected text is upstream Pi 0.87.1's rendering for the same inputs,
// with only the product name and documentation location changed (D22).
func TestBuildDefaultPromptMatchesUpstreamLayout(t *testing.T) {
	docs := filepath.Join("/home", "u", ".pig", "docs")
	got := BuildDefaultPrompt(Options{
		Cwd:            "/work/app",
		Tools:          []string{"read", "bash", "edit", "write"},
		ToolHints:      piSnippets,
		ToolGuidelines: map[string][]string{"read": {"Use read to examine files instead of cat or sed."}, "write": {"Use write only for new files or complete rewrites."}},
		PigDocsPath:    docs,
	})
	want := preamble + "\n\n<tools>\n" +
		"- read: Read file contents\n- bash: Execute bash commands (ls, grep, find, etc.)\n" +
		"- edit: Make precise file edits with exact text replacement, including multiple disjoint edits in one call\n- write: Create or overwrite files\n\n" +
		"In addition to the tools above, you may have access to other custom tools depending on the project.\n</tools>\n\n<rules>\n" +
		"- Use bash for file operations like ls, rg, find\n- Use read to examine files instead of cat or sed.\n- Use write only for new files or complete rewrites.\n" +
		"- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>\n\n<docs>\n" + docsSection(docs) + "\n</docs>\n\n<cwd>\n/work/app\n</cwd>"
	if got != want {
		t.Fatalf("prompt mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !strings.Contains(got, "- Main documentation: "+filepath.Join(docs, "README.md")+"\n- Additional docs: "+docs+"\n") {
		t.Errorf("docs section does not point at the local bundle:\n%s", got)
	}
}

func TestToolsWithoutSnippetsAreNotListed(t *testing.T) {
	got := BuildDefaultPrompt(Options{Tools: []string{"read", "custom"}, ToolHints: map[string]string{"read": "Read file contents"}})
	if strings.Contains(got, "- custom") || !strings.Contains(got, "<tools>\n- read: Read file contents\n\n") {
		t.Fatalf("tools section:\n%s", got)
	}
	if none := BuildDefaultPrompt(Options{}); !strings.Contains(none, "<tools>\n(none)\n\nIn addition") {
		t.Fatalf("empty tools section:\n%s", none)
	}
}

func TestRulesDeduplicateTrimAndKeepToolOrder(t *testing.T) {
	got := guidelinesFor([]string{"edit", "mcp", "read"}, map[string][]string{
		"read":   {"  Read rule  ", "Shared"},
		"edit":   {"Edit rule", "Shared", ""},
		"mcp":    {"Use mcp tool X for Y"},
		"absent": {"Never listed"},
	}, []string{"Global rule", "Edit rule"})
	want := []string{"Edit rule", "Shared", "Use mcp tool X for Y", "Read rule", "Global rule", "Be concise in your responses", "Show file paths clearly when working with files"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("rules\n got: %q\nwant: %q", got, want)
	}
}

func TestFileOperationRuleFollowsShellTools(t *testing.T) {
	cases := map[string][]string{
		"Use bash for file operations like ls, rg, find":                                        {"bash"},
		"Use PowerShell for file operations like listing, searching, and finding files":         {"powershell"},
		"Use bash or PowerShell for file operations like listing, searching, and finding files": {"bash", "powershell"},
	}
	for rule, tools := range cases {
		if got := guidelinesFor(tools, nil, nil); got[0] != rule {
			t.Errorf("%v: first rule %q, want %q", tools, got[0], rule)
		}
	}
	if got := guidelinesFor([]string{"bash", "grep"}, nil, nil); got[0] != "Be concise in your responses" {
		t.Errorf("with grep, the shell rule must be absent: %q", got)
	}
}

func TestReplaceModeUsesCustomPromptAsPreamble(t *testing.T) {
	got := BuildDefaultPrompt(Options{AppendMode: "replace", CustomPrompt: "You are a reviewer.", Tools: []string{"read"}, ToolHints: piSnippets, Cwd: "/w"})
	if got != "You are a reviewer.\n\n<cwd>\n/w\n</cwd>" {
		t.Fatalf("replace mode:\n%s", got)
	}
}

func TestAppendModeAddsAddendumBeforeProjectContext(t *testing.T) {
	got := BuildDefaultPrompt(Options{CustomPrompt: "  Extra rule.\n", ContextFiles: []struct{ Path, Content string }{{"/w/AGENTS.md", "Be kind."}, {"/w/sub/AGENTS.md", "Be brief."}}, Cwd: "/w"})
	want := "\n\n<addendum>\nExtra rule.\n</addendum>\n\n<project_context>\nProject-specific instructions and guidelines:\n\n" +
		"<project_instructions path=\"/w/AGENTS.md\">\nBe kind.\n</project_instructions>\n\n" +
		"<project_instructions path=\"/w/sub/AGENTS.md\">\nBe brief.\n</project_instructions>\n</project_context>\n\n<cwd>\n/w\n</cwd>"
	if !strings.HasSuffix(got, want) {
		t.Fatalf("addendum and project context:\n%s", got)
	}
}

func TestSkillsSectionMatchesUpstreamFormat(t *testing.T) {
	skills := []Skill{{Name: "pdf", Description: "Read <PDF> & forms", Path: "/s/pdf/SKILL.md"}, {Name: "hidden", Description: "x", Path: "/s/h", DisableModelInvocation: true}}
	got := BuildDefaultPrompt(Options{Tools: []string{"bash"}, Skills: skills})
	want := "<skills>\nThe following skills provide specialized instructions for specific tasks.\n" +
		"Use bash to load a skill's file when the task matches its description.\n" +
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.\n\n" +
		"<available_skills>\n  <skill>\n    <name>pdf</name>\n    <description>Read &lt;PDF&gt; &amp; forms</description>\n    <location>/s/pdf/SKILL.md</location>\n  </skill>\n</available_skills>\n</skills>"
	if !strings.Contains(got, want) || strings.Contains(got, "hidden") {
		t.Fatalf("skills section:\n%s", got)
	}
	if strings.Contains(BuildDefaultPrompt(Options{Tools: []string{"edit"}, Skills: skills}), "<skills>") {
		t.Error("without read or bash, upstream omits the skills section")
	}
	if !strings.Contains(BuildDefaultPrompt(Options{Tools: []string{"bash", "read"}, Skills: skills}), "Use the read tool to load") {
		t.Error("read takes precedence over bash for loading skills")
	}
}

func TestCwdUsesForwardSlashesAndIsLast(t *testing.T) {
	got := BuildDefaultPrompt(Options{Cwd: `C:\work\app`})
	if !strings.HasSuffix(got, "<cwd>\nC:/work/app\n</cwd>") || strings.Contains(got, "repository_context") {
		t.Fatalf("cwd section:\n%s", got)
	}
}
