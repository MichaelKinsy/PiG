package ai_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pi: packages/ai/src/api/openai-responses.ts

// The request payload openai-responses builds (buildParams, convertResponsesMessages, convertResponsesTools) over random transcripts, providers,
// thinking-level maps, compat flags and stream options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestOpenAIResponsesPayloadMatchesPi(t *testing.T) { checkResponsesPayload(t, "openai-responses") }

// TestAzureOpenAIResponsesPayloadMatchesPi is the same comparison for azure-openai-responses (the deployment name replaces the model id).
func TestAzureOpenAIResponsesPayloadMatchesPi(t *testing.T) {
	checkResponsesPayload(t, "azure-openai-responses")
}

// TestOpenAICodexResponsesPayloadMatchesPi is the same comparison for openai-codex-responses (ChatGPT subscription request body).
func TestOpenAICodexResponsesPayloadMatchesPi(t *testing.T) {
	checkResponsesPayload(t, "openai-codex-responses")
}

func checkResponsesPayload(t *testing.T, api string) {
	azure, codex := api == "azure-openai-responses", api == "openai-codex-responses"
	rng := rand.New(rand.NewSource(101))
	script, stream, streamSimple, apiName := "testdata/openai_responses_payload.mjs", ai.StreamOpenAIResponses, ai.StreamSimpleOpenAIResponses, ai.APIOpenAIResponses
	switch {
	case codex:
		script, stream, streamSimple, apiName = "testdata/openai_codex_responses_payload.mjs", ai.StreamOpenAICodexResponses, ai.StreamSimpleOpenAICodexResponses, ai.APIOpenAICodexResponses
	case azure:
		script, stream, streamSimple, apiName = "testdata/azure_openai_responses_payload.mjs", ai.StreamAzureOpenAIResponses, ai.StreamSimpleAzureOpenAIResponses, ai.APIAzureOpenAIResponses
	}
	g := &payloadGen{rng: rng, api: api, provider: "openai", model: "gpt-test", thinkingSignatures: []string{"", `,"thinkingSignature":"{\"type\":\"reasoning\",\"id\":\"rs_1\",\"summary\":[]}"`}}
	pick, boolean := g.pick, g.boolean
	type probe struct {
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
		p := probe{Provider: pick("openai", "xai", "github-copilot", "custom"), BaseURL: "https://api.openai.com/v1", APIKey: "fake-key", Reasoning: rng.Intn(2) == 0, Options: map[string]any{}}
		if codex {
			p.Provider, p.BaseURL, p.APIKey = "openai-codex", "https://chatgpt.com/backend-api", "x."+base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"acc"}}`))+".y"
		}
		if azure {
			p.Provider, p.BaseURL = "azure-openai-responses", "https://resource.openai.azure.com/openai/v1"
		}
		p.Input = []string{"text"}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		var compat []string
		add := func(member string) {
			if rng.Intn(4) == 0 {
				compat = append(compat, member)
			}
		}
		if !azure { // azure-openai-responses.ts buildParams reads neither supportsMaxOutputTokens nor the cache options
			add(`"supportsMaxOutputTokens":` + boolean())
		}
		add(`"supportsStrictMode":` + boolean())
		add(`"supportsMidConvoSystemMessages":` + boolean())
		add(`"supportsLongCacheRetention":` + boolean())
		add(`"supportsDeveloperRole":` + boolean())
		add(`"supportsOpenAIGrammarTools":` + boolean())
		add(`"supportsAdditionalTools":` + boolean())
		add(`"supportsToolSearch":` + boolean())
		add(`"supportsExplicitPromptCacheMode":` + boolean())
		add(`"sessionAffinityFormat":` + pick(`"openrouter"`, `"openai"`))
		if len(compat) > 0 {
			p.Compat = json.RawMessage("{" + joinStrings(compat) + "}")
		}
		switch rng.Intn(5) {
		case 0:
			p.Levels = json.RawMessage(`{"off":null}`)
		case 1:
			p.Levels = json.RawMessage(`{"off":"minimal","high":"xhigh"}`)
		case 2:
			p.Levels = json.RawMessage(`{"medium":null,"low":null}`)
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if !azure && rng.Intn(3) == 0 {
			p.Options["cacheRetention"] = pick("none", "short", "long")
		}
		if rng.Intn(3) == 0 {
			p.Options["sessionId"] = pick("s1", "session-with-a-rather-long-identifier-"+fmt.Sprint(rng.Intn(100)))
		}
		// streamSimple's options carry no serviceTier (buildBaseOptions), so only the raw stream is given one.
		if codex && !p.Simple && rng.Intn(3) == 0 {
			p.Options["textVerbosity"] = pick("low", "medium", "high")
		}
		if !azure && !p.Simple && rng.Intn(4) == 0 {
			p.Options["serviceTier"] = pick("flex", "priority", "auto", "default")
		}
		if rng.Intn(4) == 0 {
			p.Options["toolChoice"] = pick("auto", "required", "none")
		}
		if p.Simple {
			if rng.Intn(2) == 0 {
				p.Options["reasoning"] = pick("off", "minimal", "low", "medium", "high", "xhigh")
			}
		} else {
			if rng.Intn(2) == 0 {
				p.Options["reasoningEffort"] = pick("minimal", "low", "medium", "high")
			}
			if rng.Intn(3) == 0 {
				p.Options["reasoningSummary"] = pick("auto", "concise", "detailed")
			}
		}
		p.Context = withSystemMessages(g, g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, script, probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: "gpt-test", DisplayName: "gpt-test", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 400000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: apiName, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
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
		if v, ok := p.Options["sessionId"].(string); ok {
			options.SessionID = v
		}
		if v, ok := p.Options["serviceTier"].(string); ok {
			options.ServiceTier = v
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = v
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["reasoningEffort"].(string); ok {
			options.ReasoningEffort = v
		}
		if v, ok := p.Options["textVerbosity"].(string); ok {
			options.TextVerbosity = v
		}
		if v, ok := p.Options["reasoningSummary"].(string); ok {
			options.ReasoningSummary = v
		}
		var got string
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			encoded, err := sdkjson.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, errors.New("payload captured")
		}
		run := stream
		if p.Simple {
			run = streamSimple
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
