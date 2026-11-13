package configschema_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/configschema"
	"github.com/MichaelKinsy/PiG/internal/upstreammirror"
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
	pi, err := upstreammirror.ReadPicked(repoRoot(t), "packages/coding-agent/docs/models-configuration.md")
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
	if withoutComment(got) != d2ReferenceText(t, withoutComment(string(pi))) {
		t.Error("the rendered models.json reference differs from the page the pinned Pi renders, read through d2ReferenceTexts")
	}
}

// d2ReferenceTexts maps the fragments of Pi's page that name the product to PiG's (D2, "Product name in messages",
// docs/parity/divergences/D2.md): PiG's page is Pi's page through this table and nothing else. The schemas themselves keep
// Pi's text (TestRenderedSchemasAreByteEqualToThePinnedPiPackage).
var d2ReferenceTexts = [][2]string{
	{"Pi warms only tiers with a known lifetime.", "PiG warms only tiers with a known lifetime."},
	{"When false, pi infers stop or toolUse", "When false, PiG infers stop or toolUse"},
	{"When omitted, Pi auto-detects the format", "When omitted, PiG auto-detects the format"},
	{"for pi-controlled values.", "for PiG-controlled values."},
}

// d2ReferenceText is Pi's page with each mapped fragment replaced by PiG's. A fragment Pi's page no longer holds is a stale row.
func d2ReferenceText(t *testing.T, piPage string) string {
	t.Helper()
	for _, pair := range d2ReferenceTexts {
		if !strings.Contains(piPage, pair[0]) {
			t.Errorf("d2ReferenceTexts: Pi's page no longer says %q", pair[0])
		}
		piPage = strings.ReplaceAll(piPage, pair[0], pair[1])
	}
	return piPage
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
