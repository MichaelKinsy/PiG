package codingagent

import (
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// bug-report.ts:119 describeProvider lists Object.keys(provider.headers ?? {}).sort(); a native Provider object declares its headers itself
// (models.ts:155), so the report of an extension-registered native provider names them.
func TestBugReportProviderInfoNamesTheNativeProviderHeaders(t *testing.T) {
	registry := NewModelRegistry(t.TempDir())
	t.Cleanup(registry.CloseModelTasks)
	var checks atomic.Int32
	provider := countingNativeProvider(&checks)
	provider.ID = "headers-native"
	provider.Headers = ai.ProviderHeaders{"X-Zeta": new("z"), "X-Alpha": new("a"), "X-Removed": nil}
	if err := registry.RegisterNativeProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	info := registry.BugReportProviderInfo("headers-native")
	if info == nil || !slices.Equal(info.HeaderNames, []string{"X-Alpha", "X-Removed", "X-Zeta"}) {
		t.Fatalf("header names = %+v, want the provider's declared headers sorted", info)
	}
	plain := countingNativeProvider(&checks)
	plain.ID = "plain-native"
	if err := registry.RegisterNativeProvider(t.Context(), plain); err != nil {
		t.Fatal(err)
	}
	if info := registry.BugReportProviderInfo("plain-native"); info == nil || len(info.HeaderNames) != 0 {
		t.Fatalf("a provider without headers reports %+v, want none", info)
	}
}

// The composed Provider's headers are base?.headers (provider-composer.ts:609), so models.json provider headers are never Provider.headers: a
// models.json-only provider reports no header names, and a native provider that models.json also configures reports only its own headers.
func TestBugReportProviderInfoOmitsModelsJSONHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	config := `{"providers":{` +
		`"cfg-only":{"baseUrl":"https://cfg.example/v1","apiKey":"k","api":"openai-completions","headers":{"X-Config":"c"},"models":[{"id":"m"}]},` +
		`"headers-native":{"headers":{"X-Config":"c"}}}}`
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistryWithModelsPath(path)
	t.Cleanup(registry.CloseModelTasks)
	if info := registry.BugReportProviderInfo("cfg-only"); info == nil || len(info.HeaderNames) != 0 {
		t.Fatalf("models.json-only provider reports %+v, want no header names", info)
	}
	var checks atomic.Int32
	provider := countingNativeProvider(&checks)
	provider.ID = "headers-native"
	provider.Headers = ai.ProviderHeaders{"X-Native": new("n")}
	if err := registry.RegisterNativeProvider(t.Context(), provider); err != nil {
		t.Fatal(err)
	}
	if info := registry.BugReportProviderInfo("headers-native"); info == nil || !slices.Equal(info.HeaderNames, []string{"X-Native"}) {
		t.Fatalf("native provider also configured in models.json reports %+v, want only X-Native", info)
	}
}
