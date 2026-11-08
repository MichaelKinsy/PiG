package configschema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
)

// pi: packages/coding-agent/test/references.test.ts "generated reference pages > matches the committed pages" (regression for #10790: models.json
// fields lost their reference documentation). Pi renders the reference of the models.json fields from its schemas with
// scripts/generate-references.ts and compares the result with the committed docs/models-configuration.md. PiG has no TypeScript to run: the
// renderer is configschema.RenderReferences, over the Go schema trees, and the committed page is docs/site/docs/models-configuration.md.

func TestReferencesRenderTheModelsConfigurationPageOnly(t *testing.T) {
	rendered := configschema.RenderReferences()
	if len(rendered) != 1 || rendered[configschema.ModelsConfigurationReferencePath] == "" {
		t.Fatalf("rendered pages = %v, want only %s", keys(rendered), configschema.ModelsConfigurationReferencePath)
	}
}

func TestModelsConfigurationReferenceMatchesTheCommittedPage(t *testing.T) {
	for path, want := range configschema.RenderReferences() {
		got, err := os.ReadFile(filepath.Join(repoRoot(t), "docs", "site", "docs", path))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("docs/site/docs/%s is stale; run make generate-schemas", path)
		}
	}
}

// The same page as the pinned Pi renders it: everything but the generated comment, which names each project's generator.
func TestModelsConfigurationReferenceMatchesThePinnedPiPage(t *testing.T) {
	pi, err := os.ReadFile(filepath.Join(repoRoot(t), ".upstream", "current", "packages", "coding-agent", "docs", "models-configuration.md"))
	if err != nil {
		t.Fatal(err)
	}
	withoutComment := func(page string) string {
		var kept []string
		for line := range strings.SplitSeq(page, "\n") {
			if !strings.HasPrefix(line, "<!-- This file is generated ") {
				kept = append(kept, line)
			}
		}
		return strings.Join(kept, "\n")
	}
	got := configschema.RenderReferences()[configschema.ModelsConfigurationReferencePath]
	if withoutComment(got) != withoutComment(string(pi)) {
		t.Error("the rendered models.json reference differs from the page the pinned Pi renders")
	}
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
