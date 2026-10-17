//go:build !pig_strip_docs

package prompts

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/pigstrip"
)

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
		"- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>\n\n<docs>\n" + docsSectionAt(docs, "", "") + "\n</docs>\n\n<cwd>\n/work/app\n</cwd>"
	if got != want {
		t.Fatalf("prompt mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
	if !strings.Contains(got, "- Main documentation: "+filepath.Join(docs, "README.md")+"\n- Additional docs: "+docs+"\n") {
		t.Errorf("docs section does not point at the local bundle:\n%s", got)
	}
}

// A Piglet that strips docs gets the prompt its Binary has: no docs section, so the model is not pointed at a
// bundle that is never written. The other sections keep their order.
func TestStripDocsOmitsDocsSection(t *testing.T) {
	opts := Options{Cwd: "/w", Tools: []string{"read"}, ToolHints: piSnippets, PigDocsPath: "/d"}
	stock := BuildSystemPromptSections(opts)
	if got := sectionNames(stock); strings.Join(got, ",") != "preamble,tools,rules,docs,cwd" {
		t.Fatalf("stock sections = %v", got)
	}
	t.Cleanup(pigstrip.Strip(pigstrip.ListFeatures, pigstrip.Docs))
	stripped := BuildSystemPromptSections(opts)
	if got := sectionNames(stripped); strings.Join(got, ",") != "preamble,tools,rules,cwd" {
		t.Fatalf("stripped sections = %v", got)
	}
	if prompt := BuildDefaultPrompt(opts); strings.Contains(prompt, "<docs>") || strings.Contains(prompt, "PiG documentation") {
		t.Fatalf("stripped prompt names the docs:\n%s", prompt)
	}
}

func sectionNames(sections ai.OrderedSections) []string {
	names := make([]string, len(sections))
	for i, s := range sections {
		names[i] = s.Name
	}
	return names
}
