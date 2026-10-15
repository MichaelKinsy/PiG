package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf16"

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
		want[i] = projectErroredToolResultImages(t, want[i], p.Context)
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

// projectErroredToolResultImages applies Pi 34a8bb38 (earendil-works/pi#10794, D171) to Pi 1.2.0's payload: each image of an errored tool result is replaced with a text note, so the result is sent as one string, because Anthropic rejects non-text content when is_error is true.
// context is the probe's transcript: it tells which errored results have no text block.
func projectErroredToolResultImages(t *testing.T, payload string, context json.RawMessage) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatalf("undecodable Pi payload: %v", err)
	}
	textless := erroredResultsWithoutText(t, context)
	messages, _ := root["messages"].([]any)
	changed := false
	for _, message := range messages {
		blocks, _ := message.(map[string]any)["content"].([]any)
		for _, block := range blocks {
			result, _ := block.(map[string]any)
			if result["type"] != "tool_result" || result["is_error"] != true {
				continue
			}
			// A result without images is a string in Pi 1.2.0 and after 34a8bb38 alike ("" when it has no blocks).
			parts, isArray := result["content"].([]any)
			if !isArray {
				continue
			}
			// Pi 1.2.0 puts a "(see attached image)" text block before the images of a result without text; after 34a8bb38 every image is a
			// note, so the result has text and no placeholder. The placeholder cannot be told from a real text block in the payload, so the
			// transcript decides; TestAnthropicErroredToolResultCarriesNoImageBlock pins it.
			id, _ := result["tool_use_id"].(string)
			placeholder := textless[id]
			var lines []string
			for index, part := range parts {
				part, _ := part.(map[string]any)
				switch {
				case index == 0 && placeholder:
				case part["type"] == "image":
					lines = append(lines, "[image omitted: error tool results cannot contain images]")
				default:
					text, _ := part["text"].(string)
					lines = append(lines, text)
				}
			}
			result["content"] = strings.Join(lines, "\n")
			changed = true
		}
	}
	if !changed {
		return payload
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// erroredResultsWithoutText returns the tool_use_id, as Pi's normalizeToolCallId writes it (anthropic-messages.ts:1290), of every errored tool
// result in the transcript that has no text block.
func erroredResultsWithoutText(t *testing.T, context json.RawMessage) map[string]bool {
	t.Helper()
	var transcript struct {
		Messages []struct {
			Role       string          `json:"role"`
			ToolCallID string          `json:"toolCallId"`
			IsError    bool            `json:"isError"`
			Content    json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := sdkjson.Unmarshal(context, &transcript); err != nil {
		t.Fatal(err)
	}
	textless := map[string]bool{}
	for _, message := range transcript.Messages {
		if message.Role != "toolResult" || !message.IsError {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
		}
		if err := sdkjson.Unmarshal(message.Content, &blocks); err != nil {
			t.Fatal(err)
		}
		hasText := false
		for _, block := range blocks {
			hasText = hasText || block.Type == "text"
		}
		if !hasText {
			// Pi normalizes the id only when the transcript came from another provider, so both forms are keys.
			textless[message.ToolCallID] = true
			textless[normalizeAnthropicToolCallID(message.ToolCallID)] = true
		}
	}
	return textless
}

// normalizeAnthropicToolCallID is anthropic-messages.ts normalizeToolCallId: id.replace(/[^a-zA-Z0-9_-]/g, "_").slice(0, 64), over UTF-16 units.
func normalizeAnthropicToolCallID(id string) string {
	units := utf16.Encode([]rune(id))
	for i, unit := range units {
		if !(unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '_' || unit == '-') {
			units[i] = '_'
		}
	}
	if len(units) > 64 {
		units = units[:64]
	}
	return string(utf16.Decode(units))
}
