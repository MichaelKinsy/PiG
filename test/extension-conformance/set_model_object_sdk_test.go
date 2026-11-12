package extensionconformance

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's pi.setModel(model) makes the object it is given the Session's model (agent-session.ts:2644-2652 `this.agent.state.model = model`; :3583-3587 the
// extension binding answers false without configured auth for model.provider and true otherwise). Every SDK passes the object: the Session holds the
// members the extension left on it, which no registry lookup by provider and id could produce (a context window of 4321, a gateway base URL).
func TestSetModelTakesTheObjectAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Setenv("ANTHROPIC_API_KEY", "conformance-key")
	t.Setenv("OPENAI_API_KEY", "")
	const handBuilt = `{"id":"claude-handmade-9","name":"Handmade","api":"anthropic-messages","provider":"anthropic","baseUrl":"https://gateway.invalid/v1","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":4321,"maxTokens":99}`
	for _, sdk := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(sdk.name, func(t *testing.T) {
			h := sdk.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			if h.bridge == nil {
				t.Skip("no host bridge for " + sdk.name)
			}
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			start := ai.GetBuiltinModel("anthropic", "claude-sonnet-4-5")
			session, err := coding.NewSession(services, coding.SessionOptions{NoSession: true, SkipBuiltinTools: true, Model: start})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			coding.BindSessionExtensionActions(nil, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)
			command, ok := findCommand(h.runner, "set_model_object")
			if !ok {
				t.Fatal("the fixture has no set_model_object command")
			}
			listed := services.ModelRuntime().GetModel("anthropic", "claude-haiku-4-5")
			if listed == nil {
				t.Fatal("the registry lists no anthropic/claude-haiku-4-5")
			}
			registryObject, err := extension.ModelObject(listed)
			if err != nil {
				t.Fatal(err)
			}
			var edited map[string]any
			if err := json.Unmarshal(registryObject, &edited); err != nil {
				t.Fatal(err)
			}
			edited["contextWindow"], edited["maxTokens"] = 4321, 1234
			editedObject, err := json.Marshal(edited)
			if err != nil {
				t.Fatal(err)
			}
			set := func(object string) string {
				t.Helper()
				h.ui.ClearRecorded()
				if err := command.Handler(context.Background(), object); err != nil {
					t.Fatalf("set_model_object %s: %v", object, err)
				}
				answer := ""
				waitFor(t, func() bool {
					recorded := h.ui.Recorded()
					i := slices.IndexFunc(recorded, func(s string) bool { return strings.Contains(s, "setModelObject=") })
					if i < 0 {
						return false
					}
					_, after, _ := strings.Cut(recorded[i], "setModelObject=")
					answer = "setModelObject=" + strings.TrimSuffix(after, ":info")
					return true
				})
				return answer
			}
			model := func() string {
				m := session.Model()
				return m.ProviderMeta.ProviderID + "/" + m.ID + " window=" + itoa(m.Capabilities.ContextWindow) + " max=" + itoa(m.Capabilities.MaxOutputTokens) + " base=" + m.ProviderMeta.BaseURL
			}
			listedBase := listed.ProviderMeta.BaseURL
			for _, row := range []struct {
				name, object, wantAnswer, wantModel string
			}{
				{"registry-model-as-is", string(registryObject), "setModelObject=true", "anthropic/claude-haiku-4-5 window=" + itoa(listed.Capabilities.ContextWindow) + " max=" + itoa(listed.Capabilities.MaxOutputTokens) + " base=" + listedBase},
				{"registry-model-edited", string(editedObject), "setModelObject=true", "anthropic/claude-haiku-4-5 window=4321 max=1234 base=" + listedBase},
				{"hand-built-on-a-known-provider", handBuilt, "setModelObject=true", "anthropic/claude-handmade-9 window=4321 max=99 base=https://gateway.invalid/v1"},
				{"provider-without-credentials", `{"id":"gpt-handmade","name":"x","api":"openai-completions","provider":"openai","baseUrl":"https://gateway.invalid/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":77,"maxTokens":7}`, "setModelObject=false", "anthropic/claude-handmade-9 window=4321 max=99 base=https://gateway.invalid/v1"},
				{"unknown-provider", `{"id":"m","name":"x","api":"openai-completions","provider":"nonesuch","baseUrl":"https://gateway.invalid/v1","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":77,"maxTokens":7}`, "setModelObject=false", "anthropic/claude-handmade-9 window=4321 max=99 base=https://gateway.invalid/v1"},
				{"not-a-chat-model", `{"type":"image","id":"img","name":"x","api":"openai-images","provider":"anthropic"}`, "setModelObject=error:", "anthropic/claude-handmade-9 window=4321 max=99 base=https://gateway.invalid/v1"},
			} {
				answer := set(row.object)
				if row.wantAnswer == "setModelObject=error:" {
					if !strings.HasPrefix(answer, row.wantAnswer) || !strings.Contains(answer, "model must be a chat model object") {
						t.Errorf("%s: answer %q, want the host's rejection of a model that is not a chat model", row.name, answer)
					}
				} else if answer != row.wantAnswer {
					t.Errorf("%s: answer %q, want %q", row.name, answer, row.wantAnswer)
				}
				if got := model(); got != row.wantModel {
					t.Errorf("%s: the Session's model is %s, want %s", row.name, got, row.wantModel)
				}
			}
		})
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
