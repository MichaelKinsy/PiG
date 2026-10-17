package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/openai-completions.ts buildParams thinking formats (885-925) and buildChatTemplateValues / resolveChatTemplateKwargValue (1034-1075).

// The existing openai-completions payload oracle draws only six thinkingFormat values and no chat-template values or thinking level maps. This one covers the rest: every thinkingFormat,
// compat.chatTemplateKwargs and compat.chatTemplateArgs holding scalars, null, `{ "$var": ... }` placeholders and omitWhenOff entries, and a model thinkingLevelMap that remaps, nulls or leaves
// each level, over both stream and streamSimple options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestOpenAICompletionsThinkingFormatsPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(97))
	g := &payloadGen{rng: rng, api: "openai-completions", provider: "p", model: "m", thinkingSignatures: []string{""}}
	pick, boolean := g.pick, g.boolean
	value := func() string {
		return pick(`"x"`, `""`, `1`, `0`, `true`, `false`, `null`,
			`{"$var":"thinking.enabled"}`, `{"$var":"thinking.effort"}`, `{"$var":"thinking.budget"}`,
			`{"$var":"thinking.enabled","omitWhenOff":true}`, `{"$var":"thinking.effort","omitWhenOff":true}`, `{"$var":"thinking.budget","omitWhenOff":true}`,
			`{"omitWhenOff":true}`, `{"$var":"unknown"}`, `{}`, `[]`, `["a"]`)
	}
	values := func() string {
		var members []string
		seen := map[string]bool{}
		for range rng.Intn(4) {
			key := pick("enable_thinking", "thinking", "effort", "budget", "reasoning_effort", "preserve_thinking")
			if !seen[key] {
				seen[key] = true
				members = append(members, `"`+key+`":`+value())
			}
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	compat := func() string {
		var members []string
		add := func(member string) {
			if rng.Intn(2) == 0 {
				members = append(members, member)
			}
		}
		add(`"thinkingFormat":"` + pick("openai", "deepseek", "zai", "qwen", "chat-template", "qwen-chat-template", "string-thinking", "ant-ling", "baseten", "openrouter", "together") + `"`)
		add(`"supportsReasoningEffort":` + boolean())
		add(`"chatTemplateKwargs":` + values())
		add(`"chatTemplateArgs":` + values())
		add(`"supportsDeveloperRole":` + boolean())
		return "{" + strings.Join(members, ",") + "}"
	}
	levels := func() string {
		var members []string
		for _, level := range []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"} {
			switch rng.Intn(4) {
			case 0:
				members = append(members, `"`+level+`":null`)
			case 1:
				members = append(members, `"`+level+`":"`+pick("none", "high", "max", "", "")+`"`)
			}
		}
		return "{" + strings.Join(members, ",") + "}"
	}
	type probe struct {
		Provider  string          `json:"provider"`
		BaseURL   string          `json:"baseUrl"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Compat    json.RawMessage `json:"compat,omitempty"`
		Levels    json.RawMessage `json:"levels,omitempty"`
		Simple    bool            `json:"simple"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 3000 {
		p := probe{Provider: pick("custom", "baseten", "together", "ant-ling", "openrouter"), BaseURL: "http://127.0.0.1:9", Reasoning: rng.Intn(4) != 0, Input: []string{"text"}, Options: map[string]any{}}
		if rng.Intn(5) != 0 {
			p.Compat = json.RawMessage(compat())
		}
		if rng.Intn(2) == 0 {
			p.Levels = json.RawMessage(levels())
		}
		p.Simple = rng.Intn(2) == 0
		if p.Simple && rng.Intn(3) != 0 {
			p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
		}
		if !p.Simple && rng.Intn(3) != 0 {
			p.Options["reasoningEffort"] = pick("minimal", "low", "medium", "high", "xhigh")
		}
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		p.Context = g.context()
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/openai_completions_payload.mjs", probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: "m", DisplayName: "m", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAICompletions, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
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
		options := ai.StreamOptions{APIKey: "fake-key"}
		if v, ok := p.Options["maxTokens"].(int); ok {
			options.MaxTokens = v
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["reasoningEffort"].(string); ok {
			options.ReasoningEffort = v
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
		run := ai.StreamOpenAICompletions
		if p.Simple {
			run = ai.StreamSimpleOpenAICompletions
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
		comparePayload(t, fmt.Sprintf("probe %d provider=%s reasoning=%v simple=%v compat=%s levels=%s options=%v", i, p.Provider, p.Reasoning, p.Simple, p.Compat, p.Levels, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}
