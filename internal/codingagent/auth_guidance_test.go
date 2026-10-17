package codingagent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
	"github.com/MichaelKinsy/PiG/internal/configroot"
	"github.com/MichaelKinsy/PiG/internal/pigdocs"
)

// Login help names the providers and models pages of this build's materialized bundle (auth-guidance.ts:9-10 names Pi's package docs).
func TestProviderLoginHelpPrefersMaterializedPigDocs(t *testing.T) {
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	if err := pigdocs.EnsureSynced(pigHome); err != nil {
		t.Fatal(err)
	}

	msg := ProviderLoginHelp()
	wantProviders := filepath.Join(pigdocs.DocsDir(pigHome), "providers.md")
	wantModels := filepath.Join(pigdocs.DocsDir(pigHome), "models.md")
	if !strings.Contains(msg, wantProviders) || !strings.Contains(msg, wantModels) {
		t.Fatalf("ProviderLoginHelp = %q, want materialized pig docs %q and %q", msg, wantProviders, wantModels)
	}
	if strings.Contains(msg, ".upstream/current") {
		t.Fatalf("ProviderLoginHelp should not prefer source docs when pig docs exist: %q", msg)
	}
}

// When startup could not write the documentation bundle to a config root that held none of this build (D22), no text names it: login help keeps
// its /login sentence and names no page, PigDocsFile reports the page absent, and the system prompt has no docs section.
func TestNoTextNamesADocsBundleStartupCouldNotWrite(t *testing.T) {
	pigHome := t.TempDir()
	t.Setenv("PIG_HOME", pigHome)
	configroot.SetDocsPresent(false)
	t.Cleanup(func() { configroot.SetDocsPresent(true) })

	if path, ok := PigDocsFile("providers.md"); ok {
		t.Fatalf("PigDocsFile(providers.md) = %q, true; want false while the bundle is missing", path)
	}
	help := ProviderLoginHelp()
	if !strings.Contains(help, "/login") || strings.Contains(help, filepath.Join(pigHome, "docs")) {
		t.Fatalf("ProviderLoginHelp = %q, want the /login sentence and no docs page", help)
	}
	if prompt := prompts.BuildDefaultPrompt(prompts.Options{Cwd: "/w"}); strings.Contains(prompt, "PiG documentation") || strings.Contains(prompt, filepath.Join(pigHome, "docs")) {
		t.Fatalf("the system prompt names the missing bundle:\n%s", prompt)
	}
}
