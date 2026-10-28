package docsdrift

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// The site and the agent bundle once disagreed on six facts. Each test pins one fact in both trees, against the code or the pinned Pi.

func siteAndBundle(t *testing.T, page string) (site, bundle string) {
	t.Helper()
	s, err := os.ReadFile(filepath.Join(siteDocsDir, page))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repoRoot, bundleDirRel, page))
	if err != nil {
		t.Fatal(err)
	}
	return string(s), string(b)
}

func mustContainBoth(t *testing.T, page string, wants ...string) {
	t.Helper()
	site, bundle := siteAndBundle(t, page)
	for _, want := range wants {
		if !strings.Contains(site, want) {
			t.Errorf("site %s lacks %q", page, want)
		}
		if !strings.Contains(bundle, want) {
			t.Errorf("bundle %s lacks %q", page, want)
		}
	}
}

// Pi's sessions.md: /fork creates a new session from an earlier user message.
func TestForkIsDescribedAsAnEarlierUserMessage(t *testing.T) {
	mustContainBoth(t, "sessions.md", "| `/fork` | Select a user message and continue from before it in a new session |")
	_, bundle := siteAndBundle(t, "sessions.md")
	if strings.Contains(bundle, "Continue from the current point in a new session") {
		t.Error("the bundle describes /fork as continuing from the current point")
	}
}

// internal/configroot/configroot.go Resolve: PIG_HOME, else XDG_CONFIG_HOME/pig, else ~/.pig.
func TestPigHomeDefaultNamesXDG(t *testing.T) {
	mustContainBoth(t, "environment-variables.md", "| `PIG_HOME` | Configuration root. Default `$XDG_CONFIG_HOME/pig` when `XDG_CONFIG_HOME` is set, else `~/.pig`.")
	for _, page := range []string{"docs/site/docs/install-troubleshooting.md", bundleDirRel + "/troubleshooting.md", bundleDirRel + "/install.md"} {
		body, ok := fileBody(t, page)
		if !ok {
			t.Fatalf("no %s", page)
		}
		if !strings.Contains(body, "`$XDG_CONFIG_HOME/pig` when `XDG_CONFIG_HOME` is set, else `~/.pig`") {
			t.Errorf("%s states a PIG_HOME default without XDG_CONFIG_HOME", page)
		}
	}
}

// Pi's settings.md and the code agree: defaultThinkingLevel is medium.
func TestDefaultThinkingLevelIsMedium(t *testing.T) {
	if codingagent.DefaultThinkingLevel != "medium" {
		t.Fatalf("the code default is %q; update the docs and this test", codingagent.DefaultThinkingLevel)
	}
	mustContainBoth(t, "settings.md", "| `defaultThinkingLevel` | string | `medium` |")
}

// docs/project/CONTEXT.md names nine kinds of Resource.
func TestResourceKindListHasNineKinds(t *testing.T) {
	site, bundle := siteAndBundle(t, "concepts.md")
	for _, kind := range []string{"extension", "skill", "prompt", "hook", "MCP definition", "theme", "compatible agent declaration", "context input", "agent environment"} {
		if !strings.Contains(strings.ToLower(site), strings.ToLower(kind)) {
			t.Errorf("site concepts.md lacks the Resource kind %q", kind)
		}
		if !strings.Contains(strings.ToLower(bundle), strings.ToLower(kind)) {
			t.Errorf("bundle concepts.md lacks the Resource kind %q", kind)
		}
	}
}

func TestUpdateModelsIsDocumentedWhereUpdateIs(t *testing.T) {
	mustContainBoth(t, "packages.md", "pig update --models", "`--models` forces a model-catalog refresh")
}

// No PiG command produces a Piglet Image, so no page lists it as a current carrier.
func TestPigletImageIsPlanned(t *testing.T) {
	readme, ok := fileBody(t, bundleDirRel+"/README.md")
	if !ok {
		t.Fatal("no bundle README")
	}
	if strings.Contains(readme, "| Piglet Image | OCI materialization of Piglet component/environment closure | environment-complete carrier |") {
		t.Error("the bundle README lists the Piglet Image as a current pole")
	}
	if !strings.Contains(readme, "| Piglet Image | planned, not in this release") {
		t.Error("the bundle README must mark the Piglet Image as planned")
	}
}
