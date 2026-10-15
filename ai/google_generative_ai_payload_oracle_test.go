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

// pi: packages/ai/src/api/google-generative-ai.ts

// The request payload google-generative-ai builds (buildParams, convertMessages, convertTools) over random transcripts, model ids, thinking-level
// maps and stream options. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal as parsed JSON.
func TestGoogleGenerativeAIPayloadMatchesPi(t *testing.T) { checkGooglePayload(t, false) }

// TestGoogleVertexPayloadMatchesPi is the same comparison for google-vertex (an API key selects the Vertex client; buildParams is the same shape).
func TestGoogleVertexPayloadMatchesPi(t *testing.T) { checkGooglePayload(t, true) }

func checkGooglePayload(t *testing.T, vertex bool) {
	rng := rand.New(rand.NewSource(97))
	api, script, stream, streamSimple, apiName, providerID := "google-generative-ai", "testdata/google_generative_ai_payload.mjs", ai.StreamGoogle, ai.StreamSimpleGoogle, ai.APIGoogleGenerativeAI, "google"
	if vertex {
		api, script, stream, streamSimple, apiName, providerID = "google-vertex", "testdata/google_vertex_payload.mjs", ai.StreamGoogleVertex, ai.StreamSimpleGoogleVertex, ai.APIGoogleVertex, "google-vertex"
	}
	g := &payloadGen{rng: rng, api: api, provider: providerID, model: "gemini-test", thinkingSignatures: []string{"", `,"thinkingSignature":"QUJDRA=="`, `,"thinkingSignature":"not base64!"`}}
	pick, boolean := g.pick, g.boolean
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
	for range 700 {
		p := probe{ID: "gemini-test", Provider: providerID, BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKey: "fake-key", Reasoning: rng.Intn(2) == 0, Options: map[string]any{}}
		if rng.Intn(2) == 0 {
			p.ID = pick("gemini-2.5-pro", "gemini-2.5-flash", "gemini-3-pro-preview", "gemini-3-flash-preview", "gemini-2.0-flash", "gemma-3-27b-it", "gemini-3.1-pro-preview")
			g.model = p.ID
		} else {
			g.model = "gemini-test"
		}
		p.Input = []string{"text"}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		switch rng.Intn(6) {
		case 0:
			p.Levels = json.RawMessage(`{"off":null}`)
		case 1:
			p.Levels = json.RawMessage(`{"medium":null,"low":null}`)
		case 2:
			p.Levels = json.RawMessage(`{"off":"minimal","xhigh":"high"}`)
		}
		p.Simple = rng.Intn(2) == 0
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if rng.Intn(3) == 0 {
			p.Options["toolChoice"] = pick("auto", "any", "none")
		}
		if p.Simple {
			if rng.Intn(3) != 0 {
				p.Options["reasoning"] = pick("off", "minimal", "low", "medium", "high", "xhigh")
			}
			if rng.Intn(4) == 0 {
				p.Options["thinkingBudgets"] = map[string]int{pick("minimal", "low", "medium", "high"): 100 + rng.Intn(5000)}
			}
		} else {
			switch rng.Intn(3) {
			case 0:
				thinking := map[string]any{"enabled": true}
				switch rng.Intn(3) {
				case 0:
					thinking["level"] = pick("LOW", "HIGH", "MEDIUM", "MINIMAL")
				case 1:
					thinking["budgetTokens"] = []int{-1, 0, 128, 4096}[rng.Intn(4)]
				}
				p.Options["thinking"] = thinking
			case 1:
				p.Options["thinking"] = map[string]any{"enabled": false}
			}
		}
		_ = boolean
		p.Context = withSystemMessages(g, g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, script, probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: p.ID, DisplayName: p.ID, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 1000000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: apiName, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
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
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = v
		}
		if v, ok := p.Options["reasoning"].(string); ok {
			options.Thinking = ai.ThinkingLevel(v)
		}
		if v, ok := p.Options["thinkingBudgets"].(map[string]int); ok {
			options.ThinkingBudgets = &ai.ThinkingBudgets{}
			for level, budget := range v {
				switch level {
				case "minimal":
					options.ThinkingBudgets.Minimal = budget
				case "low":
					options.ThinkingBudgets.Low = budget
				case "medium":
					options.ThinkingBudgets.Medium = budget
				case "high":
					options.ThinkingBudgets.High = budget
				}
			}
		}
		if v, ok := p.Options["thinking"].(map[string]any); ok {
			thinking := &ai.GoogleThinkingOptions{Enabled: v["enabled"].(bool)}
			if level, ok := v["level"].(string); ok {
				thinking.Level = ai.GoogleThinkingLevel(level)
			}
			if budget, ok := v["budgetTokens"].(int); ok {
				thinking.BudgetTokens = &budget
			}
			options.GoogleThinking = thinking
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
		comparePayload(t, fmt.Sprintf("probe %d id=%s reasoning=%v simple=%v levels=%s options=%v", i, p.ID, p.Reasoning, p.Simple, p.Levels, p.Options), got, piPayloadWithD115(want[i]), &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}
