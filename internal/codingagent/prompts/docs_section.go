//go:build !pig_strip_docs

package prompts

// docs_section.go holds the system prompt's docs section. It is the boundary a
// Piglet Binary compiles out with the pig_strip_docs tag together with
// `pig docs` and the docs bundle (internal/pigdocs); docs_section_stripped.go
// replaces it so the model is not pointed at files the Binary never writes.

import (
	"path/filepath"
	"strings"

	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

// docsSections is the docs section after tools and rules, or none when a
// Piglet strips docs, as its Binary does.
func docsSections(root, readme, examples string) []section {
	if pigstrip.Has(pigstrip.ListFeatures, pigstrip.Docs) {
		return nil
	}
	return []section{{"docs", docsSectionAt(root, readme, examples)}}
}

// docsTopic is one subject the docs section names, with the strip entry that owns it, if any.
type docsTopic struct {
	text       string
	list, id   string
	headerWord bool
}

// docsTopics are the docs section's "When asked about" topics in upstream order. The header names the ones marked
// headerWord beside pig itself, its SDK and TUI.
var docsTopics = []docsTopic{
	{text: "extensions (docs/extensions.md, examples/extensions/)", headerWord: true},
	{text: "themes (docs/themes.md)", list: pigstrip.ListFeatures, id: pigstrip.Themes, headerWord: true},
	{text: "skills (docs/skills.md)", list: pigstrip.ListFeatures, id: pigstrip.Skills, headerWord: true},
	{text: "prompt templates (docs/prompt-templates.md)", list: pigstrip.ListFeatures, id: pigstrip.PromptTemplates},
	{text: "TUI components (docs/tui.md)"},
	{text: "keybindings (docs/keybindings.md)"},
	{text: "SDK integrations (docs/sdk.md)"},
	{text: "custom providers (docs/custom-provider.md)"},
	{text: "adding models (docs/models.md)"},
	{text: "pig packages (docs/packages.md)"},
	{text: "environment variables (docs/environment-variables.md)"},
	{text: "MCP servers (docs/mcp.md)", list: pigstrip.ListExtensions, id: "mcp"},
	{text: "codemode scripts and non-LLM models such as classifiers and image models (docs/codemode.md)", list: pigstrip.ListExtensions, id: "codemode"},
}

// docsSectionAt points the model at PiG's documentation in upstream's format, with the README and examples locations given; an
// empty readme is README.md inside root and empty examples is [PigExamplesLocation].
// pig additive (D92): a topic the process strips is not named.
func docsSectionAt(root, readme, examples string) string {
	if root == "" {
		root = defaultPigDocsPath()
	}
	if readme == "" {
		readme = filepath.Join(root, "README.md")
	}
	if examples == "" {
		examples = PigExamplesLocation
	}
	var topics, header []string
	for _, topic := range docsTopics {
		if topic.list != "" && pigstrip.Has(topic.list, topic.id) {
			continue
		}
		topics = append(topics, topic.text)
		if topic.headerWord {
			header = append(header, strings.Fields(topic.text)[0])
		}
	}
	// pig additive (D22): the docs section points at the materialized PiG documentation bundle.
	return "PiG documentation (read only when the user asks about pig itself, its SDK, " + strings.Join(header, ", ") + ", or TUI):\n" +
		"- Main documentation: " + readme + "\n" +
		"- Additional docs: " + root + "\n" +
		"- Examples: " + examples + " (extensions, custom tools, SDK)\n" +
		"- When reading pig docs or examples, resolve docs/... under Additional docs and examples/... under Examples, not the current working directory\n" +
		"- When asked about: " + strings.Join(topics, ", ") + "\n" +
		"- When working on pig topics, read the docs and examples, and follow .md cross-references before implementing\n" +
		"- Always read pig .md files completely and follow links to related docs (e.g., tui.md for TUI API details)"
}

// defaultPigDocsPath is the documentation bundle directory under the config root, the one codingagent.GetDocsPath names.
func defaultPigDocsPath() string { return configroot.DocsDir() }
