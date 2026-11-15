package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// The interactive mode builds the model of a selection from the registry that listed it. A replacement session (new, resume, fork) has its own services and
// so its own registry and remote-catalog copy: the replacement carries the builder over those services, not the start-up closure, so a model only that
// registry lists is built as listed instead of from another registry's state.
func TestInteractiveReplacementBuildsFromItsOwnServices(t *testing.T) {
	const row = `{"api":"openai-completions","baseUrl":"https://pi.dev/openai/v1","contextWindow":400000,"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"id":"zz-store-only-1","input":["text"],"maxTokens":64000,"name":"ZZ Store Only 1","provider":"groq","reasoning":true,"thinkingLevelMap":{"off":null},"type":"chat"}`
	home := t.TempDir()
	for key, value := range map[string]string{"HOME": home, "PIG_HOME": home, "PI_HOME": home, "PI_OFFLINE": "1", "PIG_OFFLINE": "1"} {
		t.Setenv(key, value)
	}
	services := func(withRow bool) *coding.AgentSessionServices {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"groq":{"type":"api_key","key":"gsk_FAKE123456"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if withRow {
			generatedAt := ai.GetBuiltinModelDataGeneratedAt()
			models, err := ai.DecodeStoredModels([]json.RawMessage{json.RawMessage(row)})
			if err != nil {
				t.Fatal(err)
			}
			entry := ai.ModelsStoreEntry{Models: models, CheckedAt: new(1.0), LastModified: new(*generatedAt + 1)}
			if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), "groq", entry); err != nil {
				t.Fatal(err)
			}
		}
		svcs, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: dir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { svcs.Close() })
		return svcs
	}
	startup, replacementServices := services(false), services(true)
	if startup.ModelRuntime().GetModel("groq", "zz-store-only-1") != nil || replacementServices.ModelRuntime().GetModel("groq", "zz-store-only-1") == nil {
		t.Fatal("the fixture needs the row listed by the replacement's registry only")
	}
	session, err := coding.NewSession(replacementServices, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: replacementServices.ModelRuntime().GetModel("groq", "zz-store-only-1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	replacement := (&cliRuntimeBuilder{}).interactiveReplacement(&cliBuild{Services: replacementServices, CWD: t.TempDir(), SkillLoad: &skillLoadResult{}, SourceResolver: newStartupExtensionSourceResolver(nil)}, session, nil)
	if replacement.ModelBuilder == nil {
		t.Fatal("the replacement carries no model builder: the mode would keep building from the start-up services")
	}
	built, err := replacement.ModelBuilder("groq/zz-store-only-1")
	if err != nil || built == nil || built.ProviderMeta.API != ai.APIOpenAICompletions || built.Capabilities.ContextWindow != 400000 {
		t.Fatalf("replacement builder = %v, %v; want the model its registry lists (api openai-completions, window 400000)", built, err)
	}
}
