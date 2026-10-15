package coding

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's ModelRuntime.hasConfiguredAuth reads snapshot.configuredProviders (model-runtime.ts:544-546 in 1.1.0 and 1.2.0): a credential written or
// removed after an availability pass is not seen until the next pass publishes it. Shapes: OAuth, API key, and an OpenAI-compatible provider
// with a custom base URL whose key lives in auth.json.
func TestHasConfiguredAuthAnswersFromThePublishedSnapshot(t *testing.T) {
	clearProviderEnvKeys(t)
	t.Setenv("HOME", t.TempDir())
	agent := t.TempDir()
	authPath := filepath.Join(agent, "auth.json")
	modelsPath := filepath.Join(agent, "models.json")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	const credentials = `{"anthropic":{"type":"oauth","access":"a","refresh":"r","expires":99999999999999},` +
		`"openai":{"type":"api_key","key":"o-key"},` +
		`"local-llm":{"type":"api_key","key":"l-key"}}`
	write(modelsPath, `{"providers":{"local-llm":{"baseUrl":"http://127.0.0.1:9/v1","api":"openai-completions","models":[{"id":"local-model"}]}}}`)
	write(authPath, credentials)
	store, err := ai.NewAuthStorage(authPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateModelRuntime(t.Context(), CreateModelRuntimeOptions{Credentials: store, ModelsPath: new(&modelsPath), ModelsStorePath: filepath.Join(agent, "models-store.json"), RefreshOnCreate: new(false)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	providers := []string{"anthropic", "openai", "local-llm"}
	pass := func() {
		t.Helper()
		if err := runtime.queueAvailabilityRefresh(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	expect := func(when string, want bool) {
		t.Helper()
		for _, id := range providers {
			if got := runtime.HasConfiguredAuth(id); got != want {
				t.Errorf("%s: HasConfiguredAuth(%q) = %v, Pi answers %v from the published snapshot", when, id, got, want)
			}
		}
	}
	pass()
	expect("after a pass over stored credentials", true)
	write(authPath, `{}`)
	expect("credentials removed after the pass", true)
	pass()
	expect("the pass after the removal", false)
	write(authPath, credentials)
	expect("credentials added after the pass", false)
	pass()
	expect("the pass after the addition", true)
}
