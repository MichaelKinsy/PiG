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

// pi: packages/ai/src/api/anthropic-messages.ts

// The request payload anthropic-messages builds (buildParams, convertMessages, convertTools) over random transcripts, model compat and thinking
// settings, and stream options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestAnthropicMessagesPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(91))
	g := &payloadGen{rng: rng, api: "anthropic-messages", provider: "anthropic", model: "claude-test", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`, `,"thinkingSignature":"EqQB"`, `,"redacted":true`}}
	pick, boolean := g.pick, g.boolean
	type probe struct {
		ID        string          `json:"id"`
		Provider  string          `json:"provider"`
		BaseURL   string          `json:"baseUrl"`
		APIKey    string          `json:"apiKey"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Compat    json.RawMessage `json:"compat,omitempty"`
		Levels    json.RawMessage `json:"levels,omitempty"`
		Simple    bool            `json:"simple"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 700 {
		p := probe{ID: "claude-test", Provider: "anthropic", BaseURL: "https://api.anthropic.com", APIKey: pick("sk-ant-api03-x", "sk-ant-oat01-x"), Reasoning: rng.Intn(2) == 0, Options: map[string]any{}}
		p.Input = []string{"text"}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		var compat []string
		add := func(member string) {
			if rng.Intn(3) == 0 {
				compat = append(compat, member)
			}
		}
		add(`"forceAdaptiveThinking":` + boolean())
		add(`"supportsTemperature":` + boolean())
		add(`"supportsCacheControlOnTools":` + boolean())
		add(`"supportsEagerToolInputStreaming":` + boolean())
		add(`"allowEmptySignature":` + boolean())
		add(`"supportsStrictTools":` + boolean())
		add(`"supportsLongCacheRetention":` + boolean())
		if len(compat) > 0 {
			p.Compat = json.RawMessage("{" + joinStrings(compat) + "}")
		}
		switch rng.Intn(7) {
		case 0:
			p.Levels = json.RawMessage(`{"off":null}`)
		case 1:
			p.Levels = json.RawMessage(`{"off":"none"}`)
		case 2:
			p.Levels = json.RawMessage(`{"medium":null,"low":null}`)
		case 3:
			p.Levels = json.RawMessage(`{"off":null,"minimal":null,"xhigh":"max"}`)
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if rng.Intn(3) == 0 {
			p.Options["cacheRetention"] = pick("none", "short", "long")
		}
		if p.Simple {
			if rng.Intn(2) == 0 {
				p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
			}
		} else {
			switch rng.Intn(3) {
			case 0:
				p.Options["thinkingEnabled"] = true
				if rng.Intn(2) == 0 {
					p.Options["thinkingBudgetTokens"] = 1 + rng.Intn(8000)
				}
				if rng.Intn(2) == 0 {
					p.Options["effort"] = pick("low", "medium", "high", "xhigh", "max")
				}
				if rng.Intn(3) == 0 {
					p.Options["thinkingDisplay"] = pick("summarized", "omitted")
				}
			case 1:
				p.Options["thinkingEnabled"] = false
			}
			if rng.Intn(4) == 0 {
				p.Options["toolChoice"] = pick("auto", "any", "none")
			}
			if rng.Intn(4) == 0 {
				p.Options["metadata"] = map[string]any{"user_id": pick("u1", "user-2")}
			}
		}
		p.Context = g.context()
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/anthropic_messages_payload.mjs", probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: p.ID, DisplayName: p.ID, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIAnthropicMessages, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
		if p.Compat != nil {
			model.ProviderMeta.Compat = &ai.ModelCompat{}
			if err := json.Unmarshal(p.Compat, model.ProviderMeta.Compat); err != nil {
				t.Fatal(err)
			}
		}
		if p.Levels != nil {
			if err := json.Unmarshal(p.Levels, &model.ThinkingLevelMap); err != nil {
				t.Fatal(err)
			}
		}
		options := ai.StreamOptions{APIKey: p.APIKey}
		if v, ok := p.Options["maxTokens"].(int); ok {
			options.MaxTokens = v
		}
		if v, ok := p.Options["temperature"].(float64); ok {
			options.Temperature, options.TemperatureSet = v, true
		}
		if v, ok := p.Options["cacheRetention"].(string); ok {
			options.CacheRetention = ai.CacheRetention(v)
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["thinkingEnabled"].(bool); ok {
			options.ThinkingEnabled = &v
		}
		if v, ok := p.Options["thinkingBudgetTokens"].(int); ok {
			options.ThinkingBudgetTokens = &v
		}
		if v, ok := p.Options["effort"].(string); ok {
			options.Effort = ai.AnthropicEffort(v)
		}
		if v, ok := p.Options["thinkingDisplay"].(string); ok {
			options.ThinkingDisplay = ai.AnthropicThinkingDisplay(v)
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = map[string]any{"type": v}
		}
		if v, ok := p.Options["metadata"].(map[string]any); ok {
			options.Metadata = v
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
		run := ai.StreamAnthropic
		if p.Simple {
			run = ai.StreamSimpleAnthropic
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
		comparePayload(t, fmt.Sprintf("probe %d reasoning=%v simple=%v compat=%s levels=%s options=%v", i, p.Reasoning, p.Simple, p.Compat, p.Levels, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}

func joinStrings(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += item
	}
	return out
}
