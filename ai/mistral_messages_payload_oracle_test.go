package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/mistral-conversations.ts

// The request payload mistral-conversations builds (toChatMessages, tools, tool choice, sampling) over random transcripts: text and image user content,
// thinking, errored and aborted turns, tool calls of this and another model, tool results with images, and mid-conversation system messages, with the
// stream options temperature, maxTokens, toolChoice and the thinking level of streamSimple. The payload handed to onPayload by the pinned pi-ai and by
// PiG must be equal as parsed JSON.
func TestMistralMessagesPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(127))
	g := &payloadGen{rng: rng, api: "mistral-conversations", provider: "mistral", model: "m", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`}}
	pick := g.pick
	type probe struct {
		ID        string          `json:"id"`
		Provider  string          `json:"provider"`
		BaseURL   string          `json:"baseUrl"`
		APIKey    string          `json:"apiKey"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Levels    json.RawMessage `json:"levels,omitempty"`
		Simple    bool            `json:"simple"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 1200 {
		p := probe{ID: "m", Provider: "mistral", BaseURL: "http://127.0.0.1:9", APIKey: "fake-key", Reasoning: rng.Intn(2) == 0, Input: []string{"text"}, Options: map[string]any{}}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		switch rng.Intn(5) {
		case 0:
			p.Levels = json.RawMessage(`{"off":"none","high":"high"}`)
		case 1:
			p.Levels = json.RawMessage(`{"off":null}`)
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.3, 1}[rng.Intn(3)]
		}
		if rng.Intn(3) == 0 {
			p.Options["sessionId"] = "s1"
			p.Options["cacheRetention"] = pick("none", "short", "long")
		}
		if p.Simple {
			if rng.Intn(2) == 0 {
				p.Options["reasoning"] = pick("minimal", "low", "medium", "high")
			}
		} else if rng.Intn(3) == 0 {
			p.Options["toolChoice"] = pick(`"auto"`, `"none"`, `"any"`, `"required"`, `{"type":"function","function":{"name":"tool0"}}`)
		}
		p.Context = withSystemMessages(g, g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/mistral_messages_payload.mjs", probesWithRawOptions(probes))
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: "m", DisplayName: "m", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIMistralConversations, ProviderID: p.Provider, BaseURL: "http://127.0.0.1:9", Reasoning: p.Reasoning}}
		if p.Levels != nil {
			if err := json.Unmarshal(p.Levels, &model.ThinkingLevelMap); err != nil {
				t.Fatal(err)
			}
		}
		options := ai.StreamOptions{APIKey: "fake-key"}
		if v, ok := p.Options["maxTokens"].(int); ok {
			options.MaxTokens = v
		}
		if v, ok := p.Options["temperature"].(float64); ok {
			options.Temperature, options.TemperatureSet = v, true
		}
		if v, ok := p.Options["sessionId"].(string); ok {
			options.SessionID = v
		}
		if v, ok := p.Options["cacheRetention"].(string); ok {
			options.CacheRetention = ai.CacheRetention(v)
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			var choice any
			if err := json.Unmarshal([]byte(v), &choice); err != nil {
				t.Fatal(err)
			}
			options.ToolChoice = choice
		}
		var got string
		sentinel := errors.New("payload captured")
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			encoded, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, sentinel
		}
		run := ai.StreamMistral
		if p.Simple {
			run = ai.StreamSimpleMistral
		}
		stream, err := run(payloadContext(t), model, payloadTranscript(t, p.Context), options)
		if err == nil {
			stream.Result()
		}
		if want[i] == "" {
			if got == "" {
				continue
			}
			t.Fatalf("probe %d: Pi produced no payload where PiG did: %.300s", i, got)
		}
		comparePayload(t, fmt.Sprintf("probe %d reasoning=%v simple=%v levels=%s options=%v", i, p.Reasoning, p.Simple, p.Levels, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}
