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

// pi: packages/ai/src/api/openai-completions.ts buildParams (cache retention, routing, budget field, grammar tools, finish reason, zai tool stream).

// The compat members and options the other openai-completions payload oracles never draw: supportsFinishReason, zaiToolStream, supportsOpenAIGrammarTools, supportsMidConvo*, supportsThinkingTokenBudget,
// thinkingTokenBudgetField, cacheControlFormat, supportsLongCacheRetention, openRouterRouting and vercelGatewayRouting, with the options cacheRetention, sessionId and a thinking budget override, against
// providers and base URLs that select OpenRouter, Vercel, OpenAI and Z.ai. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestOpenAICompletionsCompatAndCacheOptionsPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(101))
	g := &payloadGen{rng: rng, api: "openai-completions", provider: "p", model: "m", thinkingSignatures: []string{""}}
	pick, boolean := g.pick, g.boolean
	routing := func() string {
		return pick(`{}`, `{"only":["a","b"]}`, `{"order":["x"]}`, `{"only":[],"order":["y","z"]}`, `{"allow_fallbacks":false}`, `{"data_collection":"deny","zdr":true}`, `{"sort":"price"}`, `{"sort":{"by":"latency","partition":null}}`, `{"max_price":{"prompt":1,"completion":"2"}}`, `{"order":["o"],"only":["n"],"ignore":["i"],"quantizations":["fp8"]}`)
	}
	compat := func() string {
		var members []string
		add := func(member string) {
			if rng.Intn(3) == 0 {
				members = append(members, member)
			}
		}
		add(`"supportsFinishReason":` + boolean())
		add(`"zaiToolStream":` + boolean())
		add(`"supportsOpenAIGrammarTools":` + boolean())
		add(`"supportsMidConvoSystemMessages":` + boolean())
		add(`"supportsMidConvoToolAdditions":` + boolean())
		add(`"supportsThinkingTokenBudget":` + boolean())
		add(`"thinkingTokenBudgetField":"` + pick("thinking_token_budget", "thinking_budget", "thinking_budget_tokens") + `"`)
		add(`"cacheControlFormat":"anthropic"`)
		add(`"supportsLongCacheRetention":` + boolean())
		add(`"openRouterRouting":` + routing())
		add(`"vercelGatewayRouting":` + pick(`{}`, `{"only":["a"]}`, `{"order":["b","c"]}`, `{"only":[],"order":[]}`))
		add(`"thinkingFormat":"` + pick("openai", "zai", "openrouter", "qwen") + `"`)
		add(`"supportsStore":` + boolean())
		add(`"supportsStrictMode":` + boolean())
		return "{" + strings.Join(members, ",") + "}"
	}
	type probe struct {
		Provider  string          `json:"provider"`
		BaseURL   string          `json:"baseUrl"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Compat    json.RawMessage `json:"compat,omitempty"`
		Simple    bool            `json:"simple"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 2500 {
		p := probe{Provider: pick("custom", "openai", "openrouter", "vercel-ai-gateway", "zai", "xai", "opencode"), Reasoning: rng.Intn(2) == 0, Input: []string{"text"}, Options: map[string]any{}}
		p.BaseURL = pick("http://127.0.0.1:9", "https://api.openai.com/v1", "https://openrouter.ai/api/v1", "https://ai-gateway.vercel.sh/v1", "https://api.z.ai/api/paas/v4", "https://api.x.ai/v1", "https://opencode.ai/zen/v1")
		if rng.Intn(5) != 0 {
			p.Compat = json.RawMessage(compat())
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["cacheRetention"] = pick("none", "short", "long")
		}
		// The empty sessionId is not drawn because it differs from Pi. Pi's options.sessionId is optional, and clampOpenAIPromptCacheKey("") (openai-prompt-cache.ts:3-8) sends
		// prompt_cache_key "" under the openai-completions.ts:826-830 gate. StreamOptions.SessionID is a string, so PiG cannot tell "" from unset and sends no key.
		if rng.Intn(2) == 0 {
			p.Options["sessionId"] = pick("s1", "session-é", "a b")
		}
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if p.Simple && rng.Intn(2) == 0 {
			p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
		}
		if !p.Simple && rng.Intn(2) == 0 {
			p.Options["reasoningEffort"] = pick("minimal", "low", "medium", "high")
		}
		p.Context = withSystemMessages(g, g.context())
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
		options := ai.StreamOptions{APIKey: "fake-key"}
		if v, ok := p.Options["maxTokens"].(int); ok {
			options.MaxTokens = v
		}
		if v, ok := p.Options["cacheRetention"].(string); ok {
			options.CacheRetention = ai.CacheRetention(v)
		}
		if v, ok := p.Options["sessionId"].(string); ok {
			options.SessionID = v
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
		comparePayload(t, fmt.Sprintf("probe %d provider=%s baseUrl=%s reasoning=%v simple=%v compat=%s options=%v", i, p.Provider, p.BaseURL, p.Reasoning, p.Simple, p.Compat, p.Options), got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}

// withSystemMessages inserts zero to two system messages at random positions of a drawn context: instruction text, named sections (null removes one), tools added and tools removed. A system message
// after the first message is the mid-conversation case that compat.supportsMidConvoSystemMessages and supportsMidConvoToolAdditions decide.
func withSystemMessages(g *payloadGen, raw json.RawMessage) json.RawMessage {
	var context map[string]json.RawMessage
	if err := json.Unmarshal(raw, &context); err != nil {
		panic(err)
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(context["messages"], &messages); err != nil {
		panic(err)
	}
	for range g.rng.Intn(3) {
		members := []string{`"role":"system"`, `"content":` + g.pick(`"be brief"`, `[{"type":"text","text":"more"}]`, `""`, `"é"`), `"timestamp":1`}
		if g.rng.Intn(3) == 0 {
			members = append(members, `"sections":`+g.pick(`{"a":"x"}`, `{"a":"x","b":null}`, `{"b":"y","a":"z"}`, `{}`))
		}
		if g.rng.Intn(3) == 0 {
			members = append(members, `"toolsAdded":[{"name":"added","description":"d","parameters":{"type":"object","properties":{"q":{"type":"string"}},"required":["q"]}}]`)
		}
		if g.rng.Intn(3) == 0 {
			members = append(members, `"toolsRemoved":[{"name":"tool0"}]`)
		}
		message := json.RawMessage("{" + strings.Join(members, ",") + "}")
		at := g.rng.Intn(len(messages) + 1)
		messages = append(messages[:at], append([]json.RawMessage{message}, messages[at:]...)...)
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		panic(err)
	}
	context["messages"] = encoded
	out, err := json.Marshal(context)
	if err != nil {
		panic(err)
	}
	return out
}
