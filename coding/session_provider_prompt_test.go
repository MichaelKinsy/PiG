package coding

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/codingagent/prompts"
)

// Pi sdk.ts:356-364 awaits runner.emit; runner.ts:989-1018 ignores results,
// reports errors and continues in registration order before consuming SSE.
func TestAfterProviderResponseSession(t *testing.T) {
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Add("X-Probe", "first")
		w.Header().Add("X-Probe", "second")
		_, _ = w.Write([]byte("data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	runner := inproc.NewRunner([]extension.Extension{{Path: "/response", Handlers: map[string][]extension.HandlerFn{
		"after_provider_response": {
			func(args ...any) (any, error) {
				event := args[0].(extension.AfterProviderResponseEvent)
				if event.Type != "after_provider_response" || event.Status != 200 || event.Headers["x-probe"] != "first, second" {
					t.Errorf("response = %#v", event)
				}
				order = append(order, "response:first")
				return map[string]any{"cancel": true}, errors.New("observer failed")
			},
			func(...any) (any, error) {
				order = append(order, "response:second")
				return map[string]any{"cancel": true}, nil
			},
		},
		"message_update": {func(...any) (any, error) { order = append(order, "delta"); return nil, nil }},
	}}}, t.TempDir())
	runner.AddErrorListener(func(err *extension.ExtensionError) { order = append(order, "error:"+err.Error) })
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", BaseURL: server.URL, APIKey: "key"})
	t.Cleanup(func() { _ = provider.Close() })
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModelWithProvider(provider), Runner: runner, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	drainSessionEvents(t, session)
	if _, err := session.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	want := []string{"response:first", "error:observer failed", "response:second"}
	if len(order) < len(want)+1 || !reflect.DeepEqual(order[:len(want)], want) || order[len(want)] != "delta" {
		t.Fatalf("order = %v, want %v before delta", order, want)
	}
}

// Pi agent-session.ts:1347-1394,3178-3194 normalizes definition metadata;
// system-prompt.ts:108-116,148-152 selects and deduplicates in active-tool order. 0.99.1 also deduplicates the active names themselves (agent-session.ts:1502, `new Set(toolNames)`), so a repeated name lists once.
func TestRegisteredToolPromptContributions(t *testing.T) {
	definitions := []extension.RegisteredTool{
		{Definition: extension.ToolDefinition{Name: "read", Description: "not a snippet", PromptGuidelines: []string{"  override read  "}}},
		{Definition: extension.ToolDefinition{Name: "zeta", PromptSnippet: " \ufeffZeta\r\n  summary\t ", PromptGuidelines: []string{" shared ", "zeta rule", "shared", " "}}},
		{Definition: extension.ToolDefinition{Name: "alpha", PromptSnippet: "Alpha summary", PromptGuidelines: []string{"alpha rule", " shared "}}},
		{Definition: extension.ToolDefinition{Name: "hidden", Description: "do not use description", PromptSnippet: " \n\t "}},
	}
	for i := range definitions {
		definitions[i].Definition.Parameters = json.RawMessage(`{"type":"object"}`)
	}
	bridged, diagnostics := BridgeNewRunnerTools(definitions)
	if len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	for _, structured := range []bool{false, true} {
		t.Run(map[bool]string{false: "session-default", true: "caller-structured"}[structured], func(t *testing.T) {
			options := SessionOptions{Model: fakeModel(), Tools: bridged, NoSession: true, SkipBuiltinTools: true}
			if structured {
				options.SystemPromptSections = prompts.BuildSystemPromptSections(prompts.Options{Cwd: "/probe", Tools: []string{"read"}, ToolHints: prompts.DefaultToolSnippets()})
			}
			session, err := NewSession(newTestServices(t), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = session.Close() })
			check := func(names []string, wantTools, wantRules string) {
				t.Helper()
				session.SetActiveToolsByName(names)
				sections := map[string]string{}
				for _, s := range session.baseSystemSections {
					if s.Value != nil {
						sections[s.Name] = *s.Value
					}
				}
				wantTools = "<tools>\n" + wantTools + "\n\nIn addition to the tools above, you may have access to other custom tools depending on the project.\n</tools>"
				wantRules = "<rules>\n" + wantRules + "- Be concise in your responses\n- Show file paths clearly when working with files\n</rules>"
				if sections["tools"] != wantTools || sections["rules"] != wantRules {
					t.Fatalf("tools:\n%s\nrules:\n%s\nwant:\n%s\n%s", sections["tools"], sections["rules"], wantTools, wantRules)
				}
			}
			check([]string{"zeta", "read", "alpha", "hidden", "zeta", "unknown"}, "- zeta: Zeta summary\n- alpha: Alpha summary", "- shared\n- zeta rule\n- override read\n- alpha rule\n")
			check([]string{"alpha", "zeta"}, "- alpha: Alpha summary\n- zeta: Zeta summary", "- alpha rule\n- shared\n- zeta rule\n")
			check([]string{}, "(none)", "")
			if structured && !strings.Contains(*session.baseSystemPrompt.Load(), "/probe") {
				t.Fatal("resource sections lost")
			}
		})
	}
}
