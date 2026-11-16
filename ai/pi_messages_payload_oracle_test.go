package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"regexp"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// hookPayloadJSON is the payload a provider hands its onPayload hook as the extension host writes it to an extension in another process: a payload the provider
// holds as a map in the member order of Pi's object. A payload that is not held would be written in byte order and could not be compared with Pi's bytes.
func hookPayloadJSON(t *testing.T, payload any) string {
	t.Helper()
	encoded, held, err := ai.MarshalHookPayload(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatalf("the provider does not hold the order of its payload %T", payload)
	}
	return string(encoded)
}

// pi: packages/ai/src/api/pi-messages.ts

// The request payload pi-messages builds (model, context, options) over random transcripts and stream options. The payload handed to onPayload by
// the pinned pi-ai and by PiG must be equal as parsed JSON and in bytes: JSON.stringify writes the members in the order Pi's literal creates them.
func TestPiMessagesPayloadMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(131))
	g := &payloadGen{rng: rng, api: "pi-messages", provider: "radius", model: "auto", thinkingSignatures: []string{"", `,"thinkingSignature":"sig"`}}
	type probe struct {
		ID        string          `json:"id"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	var probes []probe
	for range 300 {
		p := probe{ID: "auto", Reasoning: rng.Intn(2) == 0, Input: []string{"text", "image"}, Options: map[string]any{}}
		if rng.Intn(2) == 0 {
			p.Options["maxTokens"] = 1 + rng.Intn(20000)
		}
		if rng.Intn(2) == 0 {
			p.Options["temperature"] = []float64{0, 0.2, 1}[rng.Intn(3)]
		}
		if rng.Intn(2) == 0 {
			p.Options["sessionId"] = g.pick("s1", "session-with-a-rather-long-identifier-36")
		}
		if rng.Intn(2) == 0 {
			p.Options["reasoning"] = g.pick("minimal", "low", "medium", "high")
		}
		if rng.Intn(2) == 0 {
			p.Options["cacheRetention"] = g.pick("none", "short", "long")
		}
		if rng.Intn(3) == 0 {
			p.Options["toolChoice"] = g.pick("auto", "any", "none", "required")
		}
		p.Context = explicitSystemMessage(g.context())
		probes = append(probes, p)
	}
	want := piPayloads(t, "testdata/pi_messages_payload.mjs", probes)
	failures, reordered := 0, 0
	classes := map[string]int{}
	for i, p := range probes {
		model := &ai.Model{ID: p.ID, DisplayName: p.ID, Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIPiMessages, ProviderID: "radius", BaseURL: "http://127.0.0.1:9", Reasoning: p.Reasoning}}
		provider := ai.NewPiMessagesProvider(ai.PiMessagesConfig{BaseURL: "http://127.0.0.1:9", APIKey: "fake-key", Model: p.ID, ProviderID: "radius", ModelMetadata: model})
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
		if v, ok := p.Options["cacheRetention"].(string); ok {
			options.CacheRetention = ai.CacheRetention(v)
		}
		if v, ok := p.Options["toolChoice"].(string); ok {
			options.ToolChoice = v
		}
		var got string
		options.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			got = hookPayloadJSON(t, payload)
			return nil, errors.New("payload captured")
		}
		stream, err := provider.Stream(payloadContext(t), payloadTranscript(t, p.Context), options)
		if err == nil {
			stream.Result()
		}
		if want[i] == "" {
			if got == "" {
				continue
			}
			t.Fatalf("probe %d: Pi produced no payload where PiG did: %.300s", i, got)
		}
		label := fmt.Sprintf("probe %d reasoning=%v options=%v", i, p.Reasoning, p.Options)
		comparePayload(t, label, got, want[i], &failures, classes)
		comparePayloadBytes(t, label, got, want[i], &reordered)
	}
	if failures > 0 {
		t.Errorf("%d of %d probes differ from Pi; differing members: %v", failures, len(probes), classes)
	}
	if reordered > 0 {
		t.Errorf("%d of %d probes write Pi's payload in another member order", reordered, len(probes))
	}
}

// explicitSystemMessage turns the legacy systemPrompt and tools of a drawn context into the system message the agent loop writes, with its members in the order
// withToolChanges gives them (agent-loop.ts:377-383: the message's own members, then toolsAdded). Pi's normalizeContext makes the same message from them with
// toolsAdded before the timestamp, which is a member order of that conversion and not of a conversation a session holds.
func explicitSystemMessage(raw json.RawMessage) json.RawMessage {
	var context map[string]json.RawMessage
	if err := json.Unmarshal(raw, &context); err != nil {
		panic(err)
	}
	prompt, tools := context["systemPrompt"], context["tools"]
	delete(context, "systemPrompt")
	delete(context, "tools")
	if len(prompt) == 0 && len(tools) == 0 {
		return raw
	}
	// A lone surrogate in the prompt is a difference of the message's content, not of member order (PiG writes U+FFFD where Pi's JSON.stringify writes the escape): the oracle keeps it out.
	if len(prompt) == 0 || regexp.MustCompile(`\\u[dD][89a-fA-F]`).Match(prompt) {
		prompt = json.RawMessage(`""`)
	}
	message := `{"role":"system","content":` + string(prompt) + `,"timestamp":0`
	if len(tools) > 0 && string(tools) != "[]" && string(tools) != "null" {
		message += `,"toolsAdded":` + string(tools)
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(context["messages"], &messages); err != nil {
		panic(err)
	}
	messages = append([]json.RawMessage{json.RawMessage(message + "}")}, messages...)
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
