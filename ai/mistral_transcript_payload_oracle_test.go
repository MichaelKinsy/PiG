package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pi: packages/ai/src/api/mistral-conversations.ts

// The request payload mistral-conversations builds (buildChatPayload, toChatMessages, tool call id normalization) over random transcripts, with and
// without image input, tools and stream options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestMistralTranscriptPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(113))
	g := &payloadGen{rng: rng, api: "mistral-conversations", provider: "mistral", model: "mistral-test", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`}}
	type probe struct {
		ID        string          `json:"id"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Simple    bool            `json:"simple"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 700 {
		p := probe{ID: "mistral-test", Reasoning: rng.Intn(2) == 0, Input: []string{"text"}, Simple: rng.Intn(2) == 0, Options: map[string]any{}}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if rng.Intn(3) == 0 {
			p.Options["sessionId"] = g.pick("s1", "session-with-a-rather-long-identifier-36")
		}
		if p.Simple {
			if rng.Intn(2) == 0 {
				p.Options["reasoning"] = g.pick("minimal", "low", "medium", "high")
			}
		} else if rng.Intn(3) == 0 {
			p.Options["toolChoice"] = g.pick("auto", "any", "none", "required")
		}
		p.Context = g.context()
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/mistral_transcript_payload.mjs", probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: p.ID, DisplayName: p.ID, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: p.Reasoning}}
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
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = v
		}
		var got string
		sentinel := errors.New("payload captured")
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			encoded, err := sdkjson.Marshal(payload)
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
		comparePayload(t, fmt.Sprintf("probe %d reasoning=%v simple=%v input=%v options=%v", i, p.Reasoning, p.Simple, p.Input, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}
