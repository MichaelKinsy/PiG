package ai

import (
	"bytes"
	"encoding/json"
	"errors"
	"os/exec"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type mistralPayloadOptions struct {
	Reasoning       string   `json:"reasoning,omitempty"`
	PromptMode      string   `json:"promptMode,omitempty"`
	ReasoningEffort string   `json:"reasoningEffort,omitempty"`
	SessionID       string   `json:"sessionId,omitempty"`
	CacheRetention  string   `json:"cacheRetention,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxTokens       *int     `json:"maxTokens,omitempty"`
}

type mistralPayloadProbe struct {
	Simple    bool                  `json:"simple"`
	Reasoning bool                  `json:"reasoning"`
	Levels    map[string]*string    `json:"levels"`
	Options   mistralPayloadOptions `json:"options"`
}

// pi: packages/ai/src/api/mistral-conversations.ts

// MistralOptions.promptMode and reasoningEffort (mistral-conversations.ts:82-83, 202-215, 530-531): streamSimple derives them from the reasoning level
// and the model's thinking level map; stream passes the raw options through. The pinned pi-ai and PiG hand the SDK payload to onPayload for a grid of
// model shapes, reasoning levels and raw options, and the payloads must carry the same fields with the same values (the member order of PiG's request struct is not compared).
func TestMistralPayloadModeAndEffortMatchPi(t *testing.T) {
	str := func(s string) *string { return &s }
	levelMaps := map[string]map[string]*string{
		"none":     nil,
		"noneHigh": {"off": str("none"), "minimal": nil, "low": nil, "medium": nil, "high": str("high"), "xhigh": nil, "max": nil},
		"glm53":    {"off": nil, "minimal": nil, "low": str("low"), "medium": nil, "high": str("high"), "xhigh": nil, "max": str("max")},
		"empty":    {},
		"offOnly":  {"off": str("none")},
		"offNull":  {"off": nil},
		"mapped":   {"minimal": str("minimal"), "low": str("low"), "medium": str("medium"), "high": str("high"), "xhigh": str("xhigh"), "max": str("max")},
	}
	var probes []mistralPayloadProbe
	for _, simple := range []bool{true, false} {
		for _, reasoning := range []bool{true, false} {
			for name, levels := range levelMaps {
				for _, level := range []string{"", "off", "minimal", "low", "medium", "high", "xhigh", "max"} {
					probes = append(probes, mistralPayloadProbe{Simple: simple, Reasoning: reasoning, Levels: levels, Options: mistralPayloadOptions{Reasoning: level}})
				}
				_ = name
			}
			for _, raw := range []mistralPayloadOptions{
				{PromptMode: "reasoning"}, {ReasoningEffort: "high"}, {ReasoningEffort: "bogus"}, {PromptMode: "reasoning", ReasoningEffort: "none"},
				{Reasoning: "high", PromptMode: "reasoning", ReasoningEffort: "low"}, {SessionID: "s1"}, {SessionID: "s1", CacheRetention: "none"}, {SessionID: "s1", CacheRetention: "short"},
				{SessionID: "s1", CacheRetention: "long"}, {CacheRetention: "long"}, {Temperature: new(0.0)}, {Temperature: new(0.7), MaxTokens: new(100)},
			} {
				for _, levels := range []map[string]*string{nil, levelMaps["noneHigh"]} {
					probes = append(probes, mistralPayloadProbe{Simple: simple, Reasoning: reasoning, Levels: levels, Options: raw})
				}
			}
		}
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/mistral_payload.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []string
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	failures := 0
	for i, probe := range probes {
		var levels ThinkingLevelMap
		if probe.Levels != nil {
			levels = ThinkingLevelMap{}
			for level, value := range probe.Levels {
				levels[ModelThinkingLevel(level)] = value
			}
		}
		model := &Model{ID: "m", DisplayName: "m", Input: []string{"text"}, ThinkingLevelMap: levels, Capabilities: ModelCapabilities{ContextWindow: 128000, MaxOutputTokens: 16384}, ProviderMeta: ProviderMetadata{API: APIMistralConversations, ProviderID: "mistral", BaseURL: "http://127.0.0.1:9", Reasoning: probe.Reasoning}}
		options := StreamOptions{APIKey: "fake-key", Thinking: ThinkingLevel(probe.Options.Reasoning), PromptMode: probe.Options.PromptMode, ReasoningEffort: probe.Options.ReasoningEffort, SessionID: probe.Options.SessionID, CacheRetention: CacheRetention(probe.Options.CacheRetention)}
		if probe.Options.Temperature != nil {
			options.Temperature, options.TemperatureSet = *probe.Options.Temperature, true
		}
		if probe.Options.MaxTokens != nil {
			options.MaxTokens = *probe.Options.MaxTokens
		}
		var got string
		sentinel := errors.New("payload captured")
		options.OnPayload = func(p any, _ *Model) (any, error) {
			encoded, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, sentinel
		}
		transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("Hello")}}})
		run := StreamMistral
		if probe.Simple {
			run = StreamSimpleMistral
		}
		stream, err := run(t.Context(), model, transcript, options)
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		stream.Result()
		if got == "" {
			t.Fatalf("probe %d: no payload captured", i)
		}
		if !sameMistralPayload(got, want[i]) {
			if failures++; failures <= 10 {
				label, _ := json.Marshal(probe)
				t.Errorf("probe %s:\n PiG %s\n Pi  %s", label, got, want[i])
			}
		}
	}
	if failures > 10 {
		t.Errorf("%d of %d probes differ from Pi", failures, len(probes))
	}
}

// sameMistralPayload compares the two payloads member by member.
func sameMistralPayload(got, want string) bool {
	var gotValues, wantValues map[string]json.RawMessage
	if json.Unmarshal([]byte(got), &gotValues) != nil || json.Unmarshal([]byte(want), &wantValues) != nil || len(gotValues) != len(wantValues) {
		return false
	}
	for key, value := range wantValues {
		if string(gotValues[key]) != string(value) {
			return false
		}
	}
	return true
}
