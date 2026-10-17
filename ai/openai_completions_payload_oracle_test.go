package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pi: packages/ai/src/api/openai-completions.ts

// The request payload openai-completions builds (buildParams, convertMessages, convertTools), over random transcripts (text and image user content,
// assistant thinking and tool calls of the same and of another model, error and aborted turns, tool results with images), tool sets, model compat
// settings, base URLs that select detected compat, and stream options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal
// as parsed JSON (member order is not compared: PiG writes the payload from structs and maps).
func TestOpenAICompletionsPayloadMatchesPi(t *testing.T) { checkOpenAICompletionsPayload(t, false) }

// TestOpenAICompletionsDetectedCompatPayloadMatchesPi is the same comparison for provider names and base URLs that select detected compat (detectCompat).
func TestOpenAICompletionsDetectedCompatPayloadMatchesPi(t *testing.T) {
	checkOpenAICompletionsPayload(t, true)
}

func checkOpenAICompletionsPayload(t *testing.T, detected bool) {
	rng := rand.New(rand.NewSource(83))
	g := &payloadGen{rng: rng, api: "openai-completions", provider: "p", model: "m", thinkingSignatures: []string{"", `,"thinkingSignature":"reasoning_content"`, `,"thinkingSignature":"reasoning"`, `,"thinkingSignature":"sig"`}}
	pick, boolean := g.pick, g.boolean
	compat := func() string {
		if rng.Intn(3) == 0 {
			return ""
		}
		var members []string
		add := func(member string) {
			if rng.Intn(3) == 0 {
				members = append(members, member)
			}
		}
		add(`"supportsDeveloperRole":` + boolean())
		add(`"supportsReasoningEffort":` + boolean())
		add(`"supportsStore":` + boolean())
		add(`"supportsUsageInStreaming":` + boolean())
		add(`"maxTokensField":"` + pick("max_tokens", "max_completion_tokens") + `"`)
		add(`"requiresToolResultName":` + boolean())
		add(`"requiresAssistantAfterToolResult":` + boolean())
		add(`"requiresThinkingAsText":` + boolean())
		add(`"requiresReasoningContentOnAssistantMessages":` + boolean())
		add(`"thinkingFormat":"` + pick("openai", "deepseek", "zai", "qwen", "qwen-chat-template", "openrouter") + `"`)
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
	for range 700 {
		p := probe{Provider: "custom", BaseURL: "http://127.0.0.1:9", Reasoning: rng.Intn(2) == 0, Options: map[string]any{}}
		if detected {
			p.Provider = pick("openai", "openrouter", "deepseek", "zai", "xai", "groq", "together", "moonshotai", "nvidia", "cerebras", "ollama", "custom")
			p.BaseURL = pick("http://127.0.0.1:9", "https://api.openai.com/v1", "https://openrouter.ai/api/v1", "https://api.deepseek.com", "https://api.z.ai/api/paas/v4", "https://open.bigmodel.cn/api", "https://api.x.ai/v1", "https://api.groq.com/openai/v1", "https://api.together.xyz/v1", "https://api.moonshot.ai/v1", "https://integrate.api.nvidia.com/v1", "https://api.cerebras.ai/v1", "http://localhost:11434/v1", "https://api.ant-ling.com/v1", "https://gateway.ai.cloudflare.com/v1")
		}
		p.Input = []string{"text"}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		if c := compat(); c != "" {
			p.Compat = json.RawMessage(c)
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(5000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if p.Simple && rng.Intn(2) == 0 {
			p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
		}
		if !p.Simple && rng.Intn(2) == 0 {
			p.Options["reasoningEffort"] = pick("minimal", "low", "medium", "high")
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
		options := ai.StreamOptions{APIKey: "fake-key"}
		if v, ok := p.Options["maxTokens"].(int); ok {
			options.MaxTokens = v
		}
		if v, ok := p.Options["temperature"].(float64); ok {
			options.Temperature, options.TemperatureSet = v, true
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
			encoded, err := sdkjson.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, sentinel
		}
		transcript := payloadTranscript(t, p.Context)
		run := ai.StreamOpenAICompletions
		if p.Simple {
			run = ai.StreamSimpleOpenAICompletions
		}
		stream, err := run(payloadContext(t), model, transcript, options)
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
