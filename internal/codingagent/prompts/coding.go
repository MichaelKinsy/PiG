// Package prompts builds the default coding-agent system prompt.
//
// It mirrors upstream core/system-prompt.js as of Pi 0.87.1: an untagged
// preamble, then tagged sections in upstream order (tools, rules, docs,
// addendum, project_context, skills, cwd). The model receives this text, so it
// matches upstream byte for byte apart from the product name and the
// documentation location (divergence D22). BuildDefaultPrompt's callers pass the
// same inputs upstream's agent session collects: selected tools, their prompt
// snippets and guidelines, context files, and skills.
package prompts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/ai"
)

// Skill is the subset of a discovered skill that the prompt lists.
type Skill struct {
	Name        string
	Description string
	Path        string
	// DisableModelInvocation hides the skill from the model, as upstream's
	// disable-model-invocation frontmatter does.
	DisableModelInvocation bool
}

// Options configures BuildDefaultPrompt.
type Options struct {
	// Cwd is the working directory shown to the model.
	Cwd string
	// Tools are the selected tool names in the order the model sees them.
	Tools []string
	// ToolHints maps a tool name to its prompt snippet. Upstream lists only
	// tools that have a snippet.
	ToolHints map[string]string
	// ToolGuidelines maps a tool name to its prompt guidelines. Guidelines of
	// selected tools become rules, deduplicated in tool order.
	ToolGuidelines map[string][]string
	// PromptGuidelines are rules that do not belong to one tool.
	PromptGuidelines []string
	// Skills lists discovered skills.
	Skills []Skill
	// CustomPrompt is an agent definition's body. AppendMode decides its role.
	CustomPrompt string
	// AppendSystemPrompt is appended as an addendum after the configured preamble.
	AppendSystemPrompt string
	// ContextFiles are the loaded AGENTS.md and CLAUDE.md files.
	ContextFiles []struct{ Path, Content string }
	// PigDocsPath is the local documentation bundle synced by pigdocs. Empty
	// uses the standard config-root location.
	PigDocsPath string
	// AppendMode is "append" (default: CustomPrompt becomes the addendum
	// section) or "replace" (CustomPrompt replaces the preamble, and the tools,
	// rules, and docs sections are omitted, as upstream does for a custom
	// system prompt).
	AppendMode string
}

const preamble = "You are an expert coding assistant operating inside pig, a coding agent harness. You help users by reading files, executing commands, editing code, and writing new files."

type section struct{ name, content string }

// BuildDefaultPrompt renders the system prompt.
func BuildDefaultPrompt(o Options) string {
	return ai.GetCurrentSystemPrompt([]ai.Message{ai.SystemMessage{Content: ai.SystemText(""), Sections: BuildSystemPromptSections(o)}})
}

// BuildSystemPromptSections retains the ordered, independently replaceable prompt sections.
func BuildSystemPromptSections(o Options) ai.OrderedSections {
	head := preamble
	var sections []section
	if o.AppendMode == "replace" && o.CustomPrompt != "" {
		head = o.CustomPrompt
	} else {
		var tools []string
		for _, name := range o.Tools {
			if hint := o.ToolHints[name]; hint != "" {
				tools = append(tools, "- "+name+": "+hint)
			}
		}
		list := "(none)"
		if len(tools) > 0 {
			list = strings.Join(tools, "\n")
		}
		sections = append(sections,
			section{"tools", list + "\n\nIn addition to the tools above, you may have access to other custom tools depending on the project."},
			section{"rules", renderRules(o.Tools, o.ToolGuidelines, o.PromptGuidelines)},
			section{"docs", docsSection(o.PigDocsPath)},
		)
		if addendum := strings.TrimSpace(o.CustomPrompt); addendum != "" {
			sections = append(sections, section{"addendum", addendum})
		}
	}
	if o.AppendSystemPrompt != "" {
		if len(sections) > 0 && sections[len(sections)-1].name == "addendum" {
			sections[len(sections)-1].content += "\n\n" + o.AppendSystemPrompt
		} else {
			sections = append(sections, section{"addendum", o.AppendSystemPrompt})
		}
	}
	if len(o.ContextFiles) > 0 {
		parts := []string{"Project-specific instructions and guidelines:"}
		for _, f := range o.ContextFiles {
			parts = append(parts, `<project_instructions path="`+f.Path+`">`+"\n"+f.Content+"\n</project_instructions>")
		}
		sections = append(sections, section{"project_context", strings.Join(parts, "\n\n")})
	}
	if readTool := skillReadTool(o.Tools); readTool != "" {
		if skills := formatSkills(o.Skills, readTool); skills != "" {
			sections = append(sections, section{"skills", skills})
		}
	}
	cwd := o.Cwd
	if cwd == "" {
		cwd = "."
	}
	sections = append(sections, section{"cwd", strings.ReplaceAll(cwd, `\`, "/")})
	out := ai.OrderedSections{{Name: "preamble", Value: new(head)}}
	for _, s := range sections {
		out = append(out, ai.PromptSection{Name: s.name, Value: new("<" + s.name + ">\n" + s.content + "\n</" + s.name + ">")})
	}
	return out
}

// docsSection points the model at PiG's documentation in upstream's format.
func docsSection(root string) string {
	if root == "" {
		root = defaultPigDocsPath()
	}
	// pig additive (D22): the docs section points at the materialized PiG documentation bundle.
	return "PiG documentation (read only when the user asks about pig itself, its SDK, extensions, themes, skills, or TUI):\n" +
		"- Main documentation: " + filepath.Join(root, "README.md") + "\n" +
		"- Additional docs: " + root + "\n" +
		"- Examples: https://github.com/MichaelKinsy/PiG/tree/main/examples (extensions, custom tools, SDK)\n" +
		"- When reading pig docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n" +
		"- When asked about: extensions (docs/extensions.md, examples/extensions/), themes (docs/themes.md), skills (docs/skills.md), prompt templates (docs/prompt-templates.md), TUI components (docs/tui.md), keybindings (docs/keybindings.md), SDK integrations (docs/sdk.md), custom providers (docs/custom-provider.md), adding models (docs/models.md), pig packages (docs/packages.md), environment variables (docs/environment-variables.md)\n" +
		"- When working on pig topics, read the docs and examples, and follow .md cross-references before implementing\n" +
		"- Always read pig .md files completely and follow links to related docs (e.g., tui.md for TUI API details)"
}

func defaultPigDocsPath() string {
	if v := os.Getenv("PIG_HOME"); v != "" {
		return filepath.Join(v, "docs")
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "pig", "docs")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pig", "docs")
}

// renderRules mirrors upstream buildRules.
func renderRules(tools []string, toolGuidelines map[string][]string, promptGuidelines []string) string {
	var lines []string
	for _, rule := range guidelinesFor(tools, toolGuidelines, promptGuidelines) {
		lines = append(lines, "- "+rule)
	}
	return strings.Join(lines, "\n")
}

func guidelinesFor(tools []string, toolGuidelines map[string][]string, promptGuidelines []string) []string {
	has := func(name string) bool { return slices.Contains(tools, name) }
	seen := map[string]bool{}
	var out []string
	add := func(rule string) {
		rule = strings.TrimSpace(rule)
		if rule != "" && !seen[rule] {
			seen[rule] = true
			out = append(out, rule)
		}
	}
	bash, powershell := has("bash"), has("powershell")
	if (bash || powershell) && !has("grep") && !has("find") && !has("ls") {
		switch {
		case bash && powershell:
			add("Use bash or PowerShell for file operations like listing, searching, and finding files")
		case powershell:
			add("Use PowerShell for file operations like listing, searching, and finding files")
		default:
			add("Use bash for file operations like ls, rg, find")
		}
	}
	for _, name := range tools {
		for _, rule := range toolGuidelines[name] {
			add(rule)
		}
	}
	for _, rule := range promptGuidelines {
		add(rule)
	}
	add("Be concise in your responses")
	add("Show file paths clearly when working with files")
	return out
}

func skillReadTool(tools []string) string {
	for _, name := range []string{"read", "bash"} {
		if slices.Contains(tools, name) {
			return name
		}
	}
	return ""
}

// formatSkills mirrors upstream formatSkillsForPrompt, trimmed as the section is.
func formatSkills(skills []Skill, readTool string) string {
	var visible []Skill
	for _, s := range skills {
		if !s.DisableModelInvocation {
			visible = append(visible, s)
		}
	}
	if len(visible) == 0 {
		return ""
	}
	load := "Use the read tool to load a skill's file when the task matches its description."
	if readTool != "read" {
		load = "Use bash to load a skill's file when the task matches its description."
	}
	lines := []string{
		"The following skills provide specialized instructions for specific tasks.",
		load,
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}
	for _, s := range visible {
		lines = append(lines, "  <skill>", "    <name>"+escapeXML(s.Name)+"</name>", "    <description>"+escapeXML(s.Description)+"</description>", "    <location>"+escapeXML(s.Path)+"</location>", "  </skill>")
	}
	lines = append(lines, "</available_skills>")
	return strings.Join(lines, "\n")
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")

func escapeXML(s string) string { return xmlEscaper.Replace(s) }
