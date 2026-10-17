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

// pi: packages/ai/src/api/anthropic-messages.ts

// The members the base anthropic-messages payload oracle never draws: compat.supportsMidConvoEffort, supportsMidConvoSystemMessages, supportsMidConvoToolChanges and allowedFallbackModels (betas, native tool
// changes, thinking-level messages, fallbacks), the options interleavedThinking, thinkingBudgets, env cache retention, object tool choices and non-string metadata, a model maxTokens other than 16384, and
// mid-conversation system messages with named sections and tool additions/removals. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestAnthropicMessagesCompatAndOptionsPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(113))
	g := &payloadGen{rng: rng, api: "anthropic-messages", provider: "anthropic", model: "claude-test", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`, `,"thinkingSignature":"EqQB"`, `,"redacted":true`}}
	pick, boolean := g.pick, g.boolean
	type probe struct {
		ID             string          `json:"id"`
		Provider       string          `json:"provider"`
		BaseURL        string          `json:"baseUrl"`
		APIKey         string          `json:"apiKey"`
		Reasoning      bool            `json:"reasoning"`
		Input          []string        `json:"input"`
		ModelMaxTokens int             `json:"modelMaxTokens"`
		Compat         json.RawMessage `json:"compat,omitempty"`
		Levels         json.RawMessage `json:"levels,omitempty"`
		Simple         bool            `json:"simple"`
		Options        map[string]any  `json:"options"`
		Context        json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 1500 {
		p := probe{ID: "claude-test", Provider: "anthropic", BaseURL: pick("https://api.anthropic.com", "https://openrouter.ai/api"), APIKey: pick("sk-ant-api03-x", "sk-ant-oat01-x"), Reasoning: rng.Intn(3) != 0, Input: []string{"text", "image"}, ModelMaxTokens: pickInt(rng, 1024, 4096, 16384, 64000), Options: map[string]any{}}
		var compat []string
		add := func(member string) {
			if rng.Intn(2) == 0 {
				compat = append(compat, member)
			}
		}
		add(`"supportsMidConvoEffort":` + boolean())
		add(`"supportsMidConvoSystemMessages":` + boolean())
		add(`"supportsMidConvoToolChanges":` + boolean())
		add(`"forceAdaptiveThinking":` + boolean())
		add(`"supportsEagerToolInputStreaming":` + boolean())
		add(`"supportsTemperature":` + boolean())
		add(`"allowedFallbackModels":` + pick(`[]`, `[{"provider":"anthropic","model":"claude-fb","cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}}]`, `[{"provider":"anthropic","model":"fb1","cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}},{"provider":"x","model":"fb2","cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0}}]`))
		if len(compat) > 0 {
			p.Compat = json.RawMessage("{" + strings.Join(compat, ",") + "}")
		}
		switch rng.Intn(8) {
		case 0:
			p.Levels = json.RawMessage(`{"off":null}`)
		case 1:
			p.Levels = json.RawMessage(`{"xhigh":"max","minimal":"low"}`)
		case 2:
			p.Levels = json.RawMessage(`{"high":"none","medium":"xhigh","low":null}`)
		case 3:
			p.Levels = json.RawMessage(`{"max":"max","xhigh":"xhigh"}`)
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
		if rng.Intn(3) == 0 {
			p.Options["env"] = map[string]string{"PI_CACHE_RETENTION": pick("long", "short", "none", "")}
		}
		if p.Simple {
			if rng.Intn(2) == 0 {
				p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
			}
			if rng.Intn(2) == 0 {
				budgets := map[string]int{}
				for _, level := range []string{"minimal", "low", "medium", "high"} {
					if rng.Intn(2) == 0 {
						budgets[level] = pickInt(rng, 0, 512, 1024, 4000, 30000)
					}
				}
				p.Options["thinkingBudgets"] = budgets
			}
		} else {
			switch rng.Intn(3) {
			case 0:
				p.Options["thinkingEnabled"] = true
				if rng.Intn(2) == 0 {
					p.Options["thinkingBudgetTokens"] = pickInt(rng, 0, 1, 2048, 8000)
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
			if rng.Intn(2) == 0 {
				p.Options["interleavedThinking"] = rng.Intn(2) == 0
			}
			if rng.Intn(3) == 0 {
				p.Options["effort"] = pick("low", "medium", "high", "xhigh", "max")
			}
			if rng.Intn(3) == 0 {
				p.Options["toolChoice"] = pick(`"auto"`, `"any"`, `"none"`, `{"type":"tool","name":"tool0"}`, `{"type":"auto","disable_parallel_tool_use":true}`)
			}
			if rng.Intn(3) == 0 {
				p.Options["metadata"] = pick(`{"user_id":"u1"}`, `{"user_id":7}`, `{"other":"x"}`, `{"user_id":null}`, `{"user_id":""}`)
			}
		}
		p.Context = withSystemMessages(g, g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/anthropic_messages_compat_options.mjs", probesWithRawOptions(probes))
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: "claude-test", DisplayName: "claude-test", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: p.ModelMaxTokens}, ProviderMeta: ai.ProviderMetadata{API: ai.APIAnthropicMessages, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
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
		if v, ok := p.Options["env"].(map[string]string); ok {
			options.Env = ai.ProviderEnv(v)
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["thinkingBudgets"].(map[string]int); ok {
			var minimal, low, medium, high *int
			for level, dst := range map[string]**int{"minimal": &minimal, "low": &low, "medium": &medium, "high": &high} {
				if budget, set := v[level]; set {
					*dst = &budget
				}
			}
			options.ThinkingBudgets = ai.NewThinkingBudgets(minimal, low, medium, high)
		}
		if v, ok := p.Options["thinkingEnabled"].(bool); ok {
			options.ThinkingEnabled = &v
		}
		if v, ok := p.Options["thinkingBudgetTokens"].(int); ok {
			options.ThinkingBudgetTokens = &v
		}
		if v, ok := p.Options["interleavedThinking"].(bool); ok {
			options.InterleavedThinking = &v
		}
		if v, ok := p.Options["effort"].(string); ok {
			options.Effort = ai.AnthropicEffort(v)
		}
		if v, ok := p.Options["thinkingDisplay"].(string); ok {
			options.ThinkingDisplay = ai.AnthropicThinkingDisplay(v)
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			var choice any
			if err := json.Unmarshal([]byte(v), &choice); err != nil {
				t.Fatal(err)
			}
			if name, isString := choice.(string); isString {
				choice = map[string]any{"type": name}
			}
			options.ToolChoice = choice
		}
		if v, ok := p.Options["metadata"].(string); ok {
			if err := json.Unmarshal([]byte(v), &options.Metadata); err != nil {
				t.Fatal(err)
			}
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
		comparePayload(t, fmt.Sprintf("probe %d reasoning=%v simple=%v maxTokens=%d compat=%s levels=%s options=%v", i, p.Reasoning, p.Simple, p.ModelMaxTokens, p.Compat, p.Levels, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}

func pickInt(rng *rand.Rand, items ...int) int { return items[rng.Intn(len(items))] }

// probesWithRawOptions re-encodes the probes with the string-valued toolChoice and metadata options as the JSON objects they stand for. The rest of each probe, the context with its section order above all, stays as drawn.
func probesWithRawOptions[T any](probes []T) []map[string]json.RawMessage {
	out := make([]map[string]json.RawMessage, len(probes))
	for i, p := range probes {
		encoded, err := json.Marshal(p)
		if err != nil {
			panic(err)
		}
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &probe); err != nil {
			panic(err)
		}
		var options map[string]json.RawMessage
		if err := json.Unmarshal(probe["options"], &options); err != nil {
			panic(err)
		}
		for _, name := range []string{"toolChoice", "metadata"} {
			var raw string
			if json.Unmarshal(options[name], &raw) == nil && options[name] != nil {
				options[name] = json.RawMessage(raw)
			}
		}
		if probe["options"], err = json.Marshal(options); err != nil {
			panic(err)
		}
		out[i] = probe
	}
	return out
}
