package codemode_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension/builtin/codemode"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// upstream CODEMODE_DOCS_PATH is join(getDocsPath(), "codemode.md"), the codemode page of the docs Pi installs. PiG
// (D22) names the codemode page of the bundle it materializes under its config root, the directory the system prompt's
// docs section names, so the page the description and the argument errors point to must ship in that bundle.
func TestDocsPathIsTheCodemodePageOfTheMaterializedBundle(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	if got, want := codemode.DocsPath(), filepath.Join(pigdocs.DocsDir(root), "codemode.md"); got != want {
		t.Fatalf("DocsPath() = %q, want %q", got, want)
	}
	page, err := pigdocs.Read("codemode.md")
	if err != nil {
		t.Fatalf("the docs bundle does not ship codemode.md: %v", err)
	}
	// docs/codemode.md "Models": the headings the argument errors name (See "Classify" and See "Generate images").
	for _, heading := range []string{"### Classify", "### Generate images"} {
		if !strings.Contains(string(page), "\n"+heading+"\n") {
			t.Errorf("codemode.md has no %q section", heading)
		}
	}
}
