package ai_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/MichaelKinsy/PiG/ai"
)

// pi: packages/ai/src/api/bedrock-converse-stream.ts

// bedrockPayloadJSON renders the ConverseStreamInput PiG hands to OnPayload as the JSON the JS SDK command input would stringify to: member names in lowerCamel
// case, unions as their one member, bytes as base64 and documents as their JSON, with unset members omitted.
func bedrockPayloadJSON(t *testing.T, value reflect.Value) any {
	t.Helper()
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if marshal := value.MethodByName("MarshalSmithyDocument"); marshal.IsValid() {
			out := marshal.Call(nil)
			if !out[1].IsNil() {
				t.Fatal(out[1].Interface())
			}
			return json.RawMessage(out[0].Bytes())
		}
		return bedrockPayloadJSON(t, value.Elem())
	}
	switch value.Kind() {
	case reflect.Struct:
		name := value.Type().Name()
		if _, member, ok := strings.Cut(name, "Member"); ok {
			if field, found := value.Type().FieldByName("Value"); found {
				return map[string]any{lowerFirst(member): bedrockPayloadJSON(t, value.FieldByIndex(field.Index))}
			}
		}
		out := map[string]any{}
		for i := range value.NumField() {
			field := value.Type().Field(i)
			if !field.IsExported() {
				continue
			}
			if item := bedrockPayloadJSON(t, value.Field(i)); item != nil {
				out[lowerFirst(field.Name)] = item
			}
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return base64.StdEncoding.EncodeToString(value.Bytes())
		}
		out := make([]any, value.Len())
		for i := range out {
			out[i] = bedrockPayloadJSON(t, value.Index(i))
		}
		return out
	case reflect.Map:
		if value.IsNil() {
			return nil
		}
		out := map[string]any{}
		for _, key := range value.MapKeys() {
			out[key.String()] = bedrockPayloadJSON(t, value.MapIndex(key))
		}
		return out
	case reflect.String:
		if value.String() == "" && value.Type().Name() != "string" {
			return nil // an unset enum
		}
		return value.String()
	case reflect.Bool:
		return value.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return value.Int()
	case reflect.Float32:
		return json.Number(strconv.FormatFloat(value.Float(), 'g', -1, 32))
	case reflect.Float64:
		return json.Number(strconv.FormatFloat(value.Float(), 'g', -1, 64))
	}
	t.Fatalf("unhandled %s", value.Type())
	return nil
}

func lowerFirst(s string) string {
	runes := []rune(s)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

// The ConverseStream command input bedrock-converse-stream builds over random transcripts (text and image user content, thinking with and without signatures,
// same-model and foreign tool calls, error and aborted turns, tool results with images), tools, model ids (Claude, Nova, inference profile ARNs), cache
// retention, reasoning, tool choice, request metadata and stream options. Pi's onPayload value and PiG's, rendered as JSON, must be equal.
func TestBedrockConversePayloadMatchesPi(t *testing.T) {
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	rng := rand.New(rand.NewSource(131))
	g := &payloadGen{rng: rng, api: "bedrock-converse-stream", provider: "amazon-bedrock", model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`, `,"redacted":true`}}
	pick := g.pick
	const arn = "arn:aws:bedrock:us-west-2:123456789012:application-inference-profile/my-profile"
	type probe struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
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
	for range 700 {
		p := probe{ID: pick("us.anthropic.claude-sonnet-4-5-20250929-v1:0", "global.anthropic.claude-opus-4-6-v1", "anthropic.claude-3-5-haiku-20241022-v1:0", "global.anthropic.claude-opus-4-8-v1", "amazon.nova-pro-v1:0", "us.amazon.nova-lite-v1:0", "meta.llama3-70b-instruct-v1:0", "us.deepseek.r1-v1:0", arn),
			BaseURL: pick("https://bedrock-runtime.us-east-1.amazonaws.com", "https://bedrock-runtime.eu-west-1.amazonaws.com"), Reasoning: rng.Intn(2) == 0, Options: map[string]any{}}
		p.Name = p.ID
		if p.ID == arn {
			p.Name = pick("Claude Sonnet 4.5", "Claude Opus 4.6", "Nova Pro")
		}
		p.Input = []string{"text"}
		if rng.Intn(2) == 0 {
			p.Input = []string{"text", "image"}
		}
		if rng.Intn(3) == 0 {
			p.Compat = json.RawMessage(`{"supportsStrictMode":` + g.boolean() + `}`)
		}
		switch rng.Intn(6) {
		case 0:
			p.Levels = json.RawMessage(`{"xhigh":"max"}`)
		case 1:
			p.Levels = json.RawMessage(`{"low":null,"minimal":"low"}`)
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
		if rng.Intn(2) == 0 {
			p.Options["reasoning"] = pick("minimal", "low", "medium", "high", "xhigh")
		}
		if rng.Intn(4) == 0 {
			p.Options["toolChoice"] = pick("auto", "any", "none", "tool0", "read")
		}
		if rng.Intn(5) == 0 {
			p.Options["requestMetadata"] = map[string]string{"team": pick("a", "b 😀"), "k": "v"}
		}
		if rng.Intn(4) == 0 {
			p.Options["interleavedThinking"] = rng.Intn(2) == 0
		}
		if rng.Intn(4) == 0 {
			p.Options["thinkingDisplay"] = pick("summarized", "omitted")
		}
		if rng.Intn(4) == 0 {
			p.Options["thinkingBudgets"] = map[string]int{"minimal": 1024 + rng.Intn(500), "low": 1600 + rng.Intn(900), "medium": 5000 + rng.Intn(3000), "high": 9000 + rng.Intn(7000)}
		}
		if rng.Intn(5) == 0 {
			p.Options["region"] = pick("us-east-1", "us-gov-west-1", "eu-west-1")
		}
		p.Context = withSystemMessages(g, g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/bedrock_payload.mjs", probes)
	failures := 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: p.ID, DisplayName: p.Name, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 200000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIBedrockConverseStream, ProviderID: "amazon-bedrock", BaseURL: p.BaseURL, Reasoning: p.Reasoning}}
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
		options := ai.StreamOptions{APIKey: "bearer-token"}
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
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = v
			if v != "auto" && v != "any" && v != "none" {
				options.ToolChoice = map[string]any{"type": "tool", "name": v}
			}
		}
		if v, ok := p.Options["requestMetadata"].(map[string]string); ok {
			options.RequestMetadata = v
		}
		if v, ok := p.Options["interleavedThinking"].(bool); ok {
			options.InterleavedThinking = &v
		}
		if v, ok := p.Options["thinkingDisplay"].(string); ok {
			options.ThinkingDisplay = ai.AnthropicThinkingDisplay(v)
		}
		if v, ok := p.Options["thinkingBudgets"].(map[string]int); ok {
			options.ThinkingBudgets = &ai.ThinkingBudgets{Minimal: v["minimal"], Low: v["low"], Medium: v["medium"], High: v["high"]}
		}
		if v, ok := p.Options["region"].(string); ok {
			options.Region = v
		}
		var got string
		sentinel := errors.New("payload captured")
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			encoded, err := json.Marshal(bedrockPayloadJSON(t, reflect.ValueOf(payload)))
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, sentinel
		}
		run := ai.BedrockConverseStreamAPI().Stream
		if p.Simple {
			run = ai.BedrockConverseStreamAPI().StreamSimple
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
		comparePayload(t, fmt.Sprintf("probe %d id=%s reasoning=%v simple=%v options=%v", i, p.ID, p.Reasoning, p.Simple, p.Options), got, piBedrockPayloadWithD116(want[i]), &failures, classes)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
}
