package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// upstream: packages/coding-agent/src/config.ts:444-456 getReadmePath/getDocsPath/getExamplesPath resolve README.md and docs under the package directory; PiG (D22) keeps its documentation bundle under the configuration root. The login help (interactive-mode.ts:6208, auth-guidance.ts:9-10) names providers.md and models.md in that directory.
func TestDocsPathsAreTheBundleUnderTheConfigRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	if got, want := GetDocsPath(), filepath.Join(home, "docs"); got != want {
		t.Fatalf("GetDocsPath() = %q, want %q", got, want)
	}
	if got, want := GetReadmePath(), filepath.Join(home, "docs", "README.md"); got != want {
		t.Fatalf("GetReadmePath() = %q, want %q", got, want)
	}
	if got := GetExamplesPath(); got != prompts.PigExamplesLocation {
		t.Fatalf("GetExamplesPath() = %q, want %q", got, prompts.PigExamplesLocation)
	}
	guidance := ProviderLoginHelp()
	for _, name := range []string{"providers.md", "models.md"} {
		if !strings.Contains(guidance, filepath.Join(GetDocsPath(), name)) {
			t.Errorf("auth guidance %q does not name %s in the docs path", guidance, name)
		}
	}
}
