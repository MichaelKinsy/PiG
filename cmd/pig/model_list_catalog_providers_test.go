package main

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// filterAllProviderEnv clears every provider credential env var that
// AuthenticatedProviders inspects, so a developer's shell keys cannot make
// list-model assertions non-deterministic.
func filterAllProviderEnv(t *testing.T) {
	t.Helper()
	for _, provider := range ai.ListRuntimeProviders() {
		for _, key := range ai.FindEnvKeys(provider, nil) {
			t.Setenv(key, "")
		}
	}
	for _, key := range []string{
		"OLLAMA_HOST",
		"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_PROFILE", "AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK", "AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
		"AWS_CONTAINER_CREDENTIALS_FULL_URI", "AWS_WEB_IDENTITY_TOKEN_FILE",
		"GOOGLE_APPLICATION_CREDENTIALS", "GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION",
	} {
		t.Setenv(key, "")
	}
}

// TestPrintModelList_OpenCodeEnvKey_ListsOpenCodeGo drives the production
// --list-models path: an OPENCODE_API_KEY must surface both opencode providers.
// This is the caller boundary for the catalog-derived auth filter.
func TestPrintModelList_OpenCodeEnvKey_ListsOpenCodeGo(t *testing.T) {
	filterAllProviderEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OPENCODE_API_KEY", "x")
	dir := t.TempDir()
	reg := codingagent.NewModelRegistry(dir)

	out := captureStdout(func() { printModelList(reg, dir, "") })

	for _, want := range []string{"opencode", "opencode-go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("printModelList with OPENCODE_API_KEY missing %q:\n%s", want, out)
		}
	}
}
