//go:build !pig_strip_docs

package prompts

import (
	"path/filepath"
	"strings"
	"testing"
)

// upstream: packages/coding-agent/src/core/system-prompt.ts:162-165: the docs section names getReadmePath() as "Main documentation" and getDocsPath() as "Additional docs".
func TestDocsSectionNamesTheGivenReadmeAndDocsPaths(t *testing.T) {
	docs := filepath.Join(t.TempDir(), "docs")
	readme := filepath.Join(t.TempDir(), "BUNDLE-README.md")
	section := docsSectionAt(docs, readme, "examples-here")
	if !strings.Contains(section, "- Main documentation: "+readme+"\n") || !strings.Contains(section, "- Additional docs: "+docs+"\n") || !strings.Contains(section, "- Examples: examples-here (extensions") {
		t.Fatalf("docs section does not name the given paths:\n%s", section)
	}
	if section := docsSectionAt(docs, "", ""); !strings.Contains(section, "- Main documentation: "+filepath.Join(docs, "README.md")+"\n") || !strings.Contains(section, "- Examples: "+PigExamplesLocation+" (") {
		t.Fatalf("an empty readme is not README.md inside the docs path:\n%s", section)
	}
}
