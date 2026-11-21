package coding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// A model the registry does not list is no model (Pi: modelRuntime.getModel and getAvailableSnapshot return nothing for it): BuildModel answers Pi's 'Model not found' for it,
// for every provider shape, instead of an object with no api, endpoint or limits that a request would send to a host the user never configured.
type unlistedShape struct{ name, provider, listedID, storeOnlyID, storeRow, auth, models string }

// The store-only id of the oauth shape is a fixture id (zz-fixture-...) that models.dev can never list, so a catalog refresh cannot make the model a built-in one.
// The expectation is Pi's: modelRuntime.getModel returns the store row's model and nothing for an unlisted id.
func unlistedShapes() []unlistedShape {
	return []unlistedShape{
		{"oauth-provider", "github-copilot", "gpt-6.1-sol", "zz-fixture-claude-fable-6",
			`{"api":"anthropic-messages","baseUrl":"https://api.individual.githubcopilot.com","compat":{"forceAdaptiveThinking":true},"contextWindow":1000000,"cost":{"input":8,"output":40,"cacheRead":0.2,"cacheWrite":10},"id":"zz-fixture-claude-fable-6","input":["text","image"],"maxTokens":128000,"name":"Claude Fable 6","provider":"github-copilot","reasoning":true,"thinkingLevelMap":{"off":null,"xhigh":"xhigh","max":"max"},"type":"chat"}`,
			`{"github-copilot":{"type":"oauth","refresh":"refresh-fake","access":"tid=0;exp=9999999999;proxy-ep=proxy.individual.githubcopilot.com;sku=seed","expires":9999999999000}}`, ""},
		{"api-key-provider", "groq", "openai/gpt-oss-120b", "zz-store-only-1",
			`{"api":"openai-completions","baseUrl":"https://pi.dev/openai/v1","contextWindow":400000,"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"id":"zz-store-only-1","input":["text"],"maxTokens":64000,"name":"ZZ Store Only 1","provider":"groq","reasoning":true,"thinkingLevelMap":{"off":null},"type":"chat"}`,
			`{"groq":{"type":"api_key","key":"gsk_FAKE123456"}}`, ""},
		{"custom-base-url-provider", "acme", "known", "",
			"", `{"acme":{"type":"api_key","key":"acme-FAKE"}}`,
			`{"providers":{"acme":{"baseUrl":"https://acme.example/v1","api":"openai-completions","apiKey":"acme-FAKE","models":[{"id":"known"}]}}}`},
	}
}

// unlistedServices makes services over a scratch agent directory holding the shape's credential, its models.json and, when storeRow is not empty, a models-store.json that carries the row.
func unlistedServices(t *testing.T, sh unlistedShape, storeRow string) *AgentSessionServices {
	t.Helper()
	dir := t.TempDir()
	for key, value := range map[string]string{"HOME": dir, "PIG_HOME": dir, "PI_HOME": dir, "PIG_CODING_AGENT_DIR": dir, "PI_CODING_AGENT_DIR": dir, "PI_OFFLINE": "", "PIG_OFFLINE": ""} {
		t.Setenv(key, value)
	}
	if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(sh.auth), 0o600); err != nil {
		t.Fatal(err)
	}
	if sh.models != "" {
		if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(sh.models), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if storeRow != "" {
		generatedAt := ai.GetBuiltinModelDataGeneratedAt()
		entry := ai.ModelsStoreEntry{Models: mustStoredModels([]json.RawMessage{json.RawMessage(storeRow)}), CheckedAt: new(1.0), LastModified: new(*generatedAt + 1)}
		if err := ai.NewFileModelsStore(filepath.Join(dir, "models-store.json")).Write(t.Context(), sh.provider, entry); err != nil {
			t.Fatal(err)
		}
	}
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	return services
}

func TestBuildModelRefusesAnIDNoRegistryLists(t *testing.T) {
	for _, sh := range unlistedShapes() {
		t.Run(sh.name, func(t *testing.T) {
			services := unlistedServices(t, sh, "")
			listed, err := BuildModel(sh.provider+"/"+sh.listedID, services)
			if err != nil || listed == nil || listed.ProviderMeta.API == "" {
				t.Fatalf("BuildModel of the listed %s/%s = %v, %v; want a model with an api", sh.provider, sh.listedID, listed, err)
			}
			model, err := BuildModel(sh.provider+"/not-listed-anywhere", services)
			want := "Model not found: " + sh.provider + "/not-listed-anywhere"
			if model != nil || err == nil || err.Error() != want {
				t.Fatalf("BuildModel of an unlisted id = %v, %v; want no model and %q (Pi: rpc-mode.ts:495 set_model)", model, err, want)
			}
		})
	}
	t.Run("provider-with-only-an-environment-key", func(t *testing.T) {
		t.Setenv("ZZUNLISTED_API_KEY", "env-key-fake")
		services := unlistedServices(t, unlistedShape{provider: "zzunlisted", auth: `{}`}, "")
		if model, err := BuildModel("zzunlisted/some-id", services); model != nil || err == nil || err.Error() != "Model not found: zzunlisted/some-id" {
			t.Fatalf("BuildModel of an id of a provider that has a key and no models = %v, %v; want Pi's 'Model not found'", model, err)
		}
	})
}

// The remote catalog's row is a listing: the object BuildModel returns is the one the runtime lists, whichever shape the provider has.
func TestBuildModelOfAStoreOnlyIDIsTheListedObject(t *testing.T) {
	for _, sh := range unlistedShapes() {
		if sh.storeOnlyID == "" {
			continue
		}
		t.Run(sh.name, func(t *testing.T) {
			services := unlistedServices(t, sh, sh.storeRow)
			listed := services.ModelRuntime().GetModel(sh.provider, sh.storeOnlyID)
			if listed == nil {
				t.Fatalf("the runtime does not list %s/%s though the store carries it", sh.provider, sh.storeOnlyID)
			}
			built, err := BuildModel(sh.provider+"/"+sh.storeOnlyID, services)
			if err != nil || built == nil {
				t.Fatalf("BuildModel = %v, %v; want the listed model", built, err)
			}
			if built.ProviderMeta.API != listed.ProviderMeta.API || built.ProviderMeta.BaseURL != listed.ProviderMeta.BaseURL || built.Capabilities.ContextWindow != listed.Capabilities.ContextWindow || built.ProviderMeta.Reasoning != listed.ProviderMeta.Reasoning || built.ProviderMeta.API == "" {
				t.Fatalf("BuildModel = api %q base %q window %d reasoning %v, the runtime lists api %q base %q window %d reasoning %v", built.ProviderMeta.API, built.ProviderMeta.BaseURL, built.Capabilities.ContextWindow, built.ProviderMeta.Reasoning, listed.ProviderMeta.API, listed.ProviderMeta.BaseURL, listed.Capabilities.ContextWindow, listed.ProviderMeta.Reasoning)
			}
		})
	}
}

// A session whose last model only the remote catalog lists comes back with that listed object; a file whose last thinking level is 'off' for a model that has no off level (the owner's files: model_change to a
// force-adaptive model, then thinking_level_change off) comes back with the level Pi gives (RUN on Pi 1.5.2: 'minimal'), and when the record is absent the model is not an object with no api.
func TestRestoreOfAStoreOnlyModelAndAPoisonedLevel(t *testing.T) {
	for _, sh := range unlistedShapes() {
		if sh.storeOnlyID == "" {
			continue
		}
		file := func(t *testing.T) string {
			ts := "2026-11-14T03:00:00.000Z"
			usage := `"usage":{"input":1,"output":1,"cacheRead":0,"cacheWrite":0,"totalTokens":2,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}`
			header, err := json.Marshal(struct {
				Type      string `json:"type"`
				Version   int    `json:"version"`
				ID        string `json:"id"`
				Timestamp string `json:"timestamp"`
				CWD       string `json:"cwd"`
			}{"session", 3, "0190a000-0000-7000-8000-000000000001", ts, t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			lines := []string{
				string(header),
				`{"type":"model_change","id":"a1","parentId":null,"timestamp":"` + ts + `","provider":"` + sh.provider + `","modelId":"` + sh.storeOnlyID + `"}`,
				`{"type":"thinking_level_change","id":"a2","parentId":"a1","timestamp":"` + ts + `","thinkingLevel":"high"}`,
				`{"type":"thinking_level_change","id":"a3","parentId":"a2","timestamp":"` + ts + `","thinkingLevel":"off"}`,
				`{"type":"message","id":"a4","parentId":"a3","timestamp":"` + ts + `","message":{"role":"user","content":[{"type":"text","text":"first"}],"timestamp":1000}}`,
				`{"type":"message","id":"a5","parentId":"a4","timestamp":"` + ts + `","message":{"role":"assistant","content":[{"type":"text","text":"reply"}],"api":"x","provider":"` + sh.provider + `","model":"` + sh.storeOnlyID + `",` + usage + `,"stopReason":"stop","timestamp":2000}}`,
			}
			path := filepath.Join(t.TempDir(), "session.jsonl")
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		}
		t.Run(sh.name+"/record-present", func(t *testing.T) {
			services := unlistedServices(t, sh, sh.storeRow)
			listed := services.ModelRuntime().GetModel(sh.provider, sh.storeOnlyID)
			session, err := NewSession(services, SessionOptions{ResumePath: file(t), SkipBuiltinTools: true})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			got := session.Model()
			if got == nil || listed == nil || got.ID != sh.storeOnlyID || got.ProviderMeta.API != listed.ProviderMeta.API || got.Capabilities.ContextWindow != listed.Capabilities.ContextWindow || got.ProviderMeta.API == "" {
				t.Fatalf("restored model = %v, want the listed %s/%s (api %q)", got, sh.provider, sh.storeOnlyID, listed.ProviderMeta.API)
			}
			want := ai.ClampThinkingLevel(listed, ai.ThinkingOff)
			if sh.provider == "github-copilot" {
				want = ai.ThinkingMinimal // Pi 1.5.2 on this file: thinking level minimal (lane BareModelCure p1.log)
			}
			if session.ThinkingLevel() != want {
				t.Fatalf("restored thinking level = %q, Pi gives %q", session.ThinkingLevel(), want)
			}
		})
		t.Run(sh.name+"/record-absent", func(t *testing.T) {
			services := unlistedServices(t, sh, "")
			session, err := NewSession(services, SessionOptions{ResumePath: file(t), SkipBuiltinTools: true, Model: nil})
			if err != nil {
				t.Logf("NewSession: %v (Pi has no model here either)", err)
				return
			}
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
			})
			if got := session.Model(); got != nil && (got.ProviderMeta.API == "" || got.ID == sh.storeOnlyID) {
				t.Fatalf("restored model = %+v with the record absent; Pi has no object for %s/%s", got, sh.provider, sh.storeOnlyID)
			}
		})
	}
}
