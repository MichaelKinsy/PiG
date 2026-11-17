package subprocess

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/requestvalidity"
)

// pi: packages/ai/src/api/google-generative-ai.ts:363-432, pi-messages.ts:393-403
//
// A provider that hands its before_provider_request hook a map (Google, Vertex, pi-messages) has no member order to give an extension in another process; the
// event the host writes to the extension carries the payload in the order of Pi's object: model, contents, config, and the config's members as Pi's literal
// creates them (the generation fields, systemInstruction, tools, toolConfig, automaticContinuation, thinkingConfig). A member an in-process handler added is
// written after those, sorted.
func TestBeforeProviderRequestWireKeepsPiMemberOrder(t *testing.T) {
	run := func(t *testing.T, provider ai.Provider, edit func(payload any)) string {
		t.Helper()
		var wire string
		stop := errors.New("payload captured")
		options := ai.StreamOptions{APIKey: "k", MaxTokens: 7, Temperature: 0.5, TemperatureSet: true, ToolChoice: "auto", OnPayload: func(payload any, _ *ai.Model) (any, error) {
			if edit != nil {
				edit(payload)
			}
			encoded, err := json.Marshal(wireEventPayload(extension.BeforeProviderRequestEvent{Type: "before_provider_request", Payload: payload}))
			if err != nil {
				t.Fatal(err)
			}
			wire = string(encoded)
			return nil, stop
		}}
		transcript := ai.NormalizeContext(ai.Context{SystemPrompt: "be brief", Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi"), Timestamp: 1}},
			Tools: []ai.ToolSchema{{Name: "t", Description: "d", Parameters: map[string]any{"type": "object"}}}})
		stream, err := provider.Stream(requestvalidity.Allow(t.Context()), transcript, options)
		if err == nil {
			stream.Result()
		}
		if wire == "" {
			t.Fatal("the hook was not called")
		}
		return wire
	}
	t.Run("google", func(t *testing.T) {
		provider := ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "k", Model: "gemini-2.5-flash", ProviderID: "google", BaseURL: "http://127.0.0.1:9"})
		want := `{"type":"before_provider_request","payload":{"model":"gemini-2.5-flash","contents":[{"role":"user","parts":[{"text":"hi"}]}],"config":{"temperature":0.5,"maxOutputTokens":7,"systemInstruction":"be brief","tools":`
		if got := run(t, provider, nil); !strings.HasPrefix(got, want) || !strings.Contains(got, `"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}},"automaticContinuation":false}}}`) {
			t.Errorf("got  %s\nwant %s...", got, want)
		}
	})
	t.Run("google, members a handler added in place", func(t *testing.T) {
		provider := ai.NewGoogleProvider(ai.GoogleConfig{APIKey: "k", Model: "gemini-2.5-flash", ProviderID: "google", BaseURL: "http://127.0.0.1:9"})
		got := run(t, provider, func(payload any) {
			object := payload.(map[string]any)
			object["zeta"] = 1
			object["alpha"] = 2
			object["config"].(map[string]any)["topP"] = 0.5
			delete(object["config"].(map[string]any), "tools")
		})
		if !strings.Contains(got, `"toolConfig":{"functionCallingConfig":{"mode":"AUTO"}},"automaticContinuation":false,"topP":0.5},"alpha":2,"zeta":1}}`) || strings.Contains(got, `"tools"`) {
			t.Errorf("got %s", got)
		}
	})
	t.Run("pi-messages", func(t *testing.T) {
		provider := ai.NewPiMessagesProvider(ai.PiMessagesConfig{BaseURL: "http://127.0.0.1:9", APIKey: "k", Model: "auto", ProviderID: "radius"})
		want := `{"type":"before_provider_request","payload":{"model":"auto","context":{"messages":[`
		if got := run(t, provider, nil); !strings.HasPrefix(got, want) || !strings.Contains(got, `"options":{"temperature":0.5,"maxTokens":7,"cacheRetention"`) && !strings.Contains(got, `"options":{"temperature":0.5,"maxTokens":7,"toolChoice"`) {
			t.Errorf("got %s", got)
		}
	})
}
