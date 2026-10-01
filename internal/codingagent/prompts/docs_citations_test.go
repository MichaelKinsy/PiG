package prompts

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// The system prompt tells the model which documentation to read for each topic. A citation of a page that the
// binary does not ship sends the model to a file that does not exist, so every cited page must be in the
// embedded docs bundle, exist in the directory `pig docs` materializes (the "Additional docs" root the prompt
// names), and resolve through `pig docs show`.
func TestDocsSectionCitesOnlyShippedPages(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", root)
	docsDir := pigdocs.DocsDir(root)
	if _, err := pigdocs.Sync(root); err != nil {
		t.Fatal(err)
	}
	shipped := pigdocs.List()
	section := docsSection(docsDir)
	cited := regexp.MustCompile(`\(docs/([A-Za-z0-9_.-]+\.md)[,)]`).FindAllStringSubmatch(section, -1)
	if len(cited) < 10 {
		t.Fatalf("found %d cited docs in the section, want at least 10; the pattern or the section changed:\n%s", len(cited), section)
	}
	pages := []string{"README.md"}
	for _, match := range cited {
		pages = append(pages, match[1])
	}
	for _, page := range pages {
		if !slices.Contains(shipped, page) {
			t.Errorf("the system prompt cites docs/%s, which the embedded docs bundle does not ship", page)
		}
		if info, err := os.Stat(filepath.Join(docsDir, page)); err != nil || info.Size() == 0 {
			t.Errorf("docs/%s is not materialized under the Additional docs root %s: %v", page, docsDir, err)
		}
		var stdout, stderr bytes.Buffer
		if code := pigdocs.RunCommand([]string{"docs", "show", strings.TrimSuffix(page, ".md")}, &stdout, &stderr); code != 0 || stdout.Len() == 0 {
			t.Errorf("pig docs show %s: code=%d stderr=%q", strings.TrimSuffix(page, ".md"), code, stderr.String())
		}
	}
	if !strings.Contains(section, filepath.Join(docsDir, "README.md")) {
		t.Errorf("the section does not name the main documentation under %s", docsDir)
	}
	for _, page := range []string{"mcp.md", "codemode.md", "virtual-models.md"} {
		if !slices.Contains(shipped, page) {
			t.Errorf("docs bundle lacks %s", page)
		}
	}
	if !strings.Contains(section, "docs/mcp.md") {
		t.Error("the documentation section no longer cites the MCP page")
	}
}
