package ai_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	sdkjson "github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// pi: packages/ai/src/api/openai-responses-shared.ts renderIncrementalToolsNotice, renderToolRemovalNotice

// toolNoticeSystem is one system message of a tool-change transcript: its text and the tool names it adds and removes.
type toolNoticeSystem struct {
	Text    string
	Added   []string
	Removed []string
}

func (s toolNoticeSystem) json(stamp int) string {
	text, _ := json.Marshal(s.Text)
	members := []string{`"role":"system"`, `"content":` + string(text), fmt.Sprintf(`"timestamp":%d`, stamp)}
	if s.Added != nil {
		var tools []string
		for _, name := range s.Added {
			quoted, _ := json.Marshal(name)
			tools = append(tools, fmt.Sprintf(`{"name":%s,"description":"d","parameters":{"type":"object","properties":{}}}`, quoted))
		}
		members = append(members, `"toolsAdded":[`+strings.Join(tools, ",")+`]`)
	}
	if s.Removed != nil {
		var tools []string
		for _, name := range s.Removed {
			quoted, _ := json.Marshal(name)
			tools = append(tools, fmt.Sprintf(`{"name":%s}`, quoted))
		}
		members = append(members, `"toolsRemoved":[`+strings.Join(tools, ",")+`]`)
	}
	return "{" + strings.Join(members, ",") + "}"
}

// toolNoticeContext is a transcript that interleaves the system messages with a user turn after each.
func toolNoticeContext(systems []toolNoticeSystem) json.RawMessage {
	var messages []string
	for i, system := range systems {
		messages = append(messages, system.json(2*i))
		if i < len(systems)-1 || i == 0 {
			messages = append(messages, fmt.Sprintf(`{"role":"user","content":"turn %d","timestamp":%d}`, i, 2*i+1))
		}
	}
	return json.RawMessage(`{"messages":[` + strings.Join(messages, ",") + `]}`)
}

// toolNoticeCases are the transcripts every branch of the two notices needs: the first additional_tools item carries no notice; each later one
// carries the incremental notice with the tools still unavailable in the order they were announced; a tool declared again leaves the list and
// returns at the end when it is removed again; a redefinition is no removal; a removal notice lists the names in the message's order.
func toolNoticeCases() map[string][]toolNoticeSystem {
	return map[string][]toolNoticeSystem{
		"first-declaration-has-no-notice":           {{Text: "base", Added: []string{"a"}}},
		"second-declaration-notice-without-removal": {{Text: "base", Added: []string{"a"}}, {Text: "", Added: []string{"b"}}},
		"third-declaration":                         {{Text: "base", Added: []string{"a"}}, {Added: []string{"b"}}, {Added: []string{"c"}}},
		"single-removal":                            {{Text: "base", Added: []string{"a", "b"}}, {Text: "update", Removed: []string{"a"}}},
		"single-removal-empty-update-text":          {{Text: "base", Added: []string{"a", "b"}}, {Text: "", Removed: []string{"a"}}},
		"multi-removal-keeps-message-order":         {{Text: "base", Added: []string{"a", "b", "c"}}, {Text: "", Removed: []string{"c", "a", "b"}}},
		"duplicate-removal-names":                   {{Text: "base", Added: []string{"a"}}, {Text: "", Removed: []string{"a", "a"}}, {Added: []string{"z"}}},
		"removal-then-load-lists-unavailable":       {{Text: "base", Added: []string{"a", "b"}}, {Removed: []string{"a"}}, {Added: []string{"c"}}},
		"two-removals-in-announced-order": {
			{Text: "base", Added: []string{"a", "b", "c"}}, {Removed: []string{"b"}}, {Removed: []string{"a"}}, {Added: []string{"d"}},
		},
		"removal-in-the-same-message-as-a-load": {
			{Text: "base", Added: []string{"a", "b"}}, {Added: []string{"c"}, Removed: []string{"b"}}, {Added: []string{"d"}},
		},
		"redeclared-tool-leaves-the-list": {
			{Text: "base", Added: []string{"a", "b"}}, {Removed: []string{"a", "b"}}, {Added: []string{"a"}}, {Added: []string{"e"}},
		},
		"redeclared-tool-returns-at-the-end": {
			{Text: "base", Added: []string{"a", "b"}}, {Removed: []string{"a"}}, {Removed: []string{"b"}}, {Added: []string{"a"}}, {Removed: []string{"a"}}, {Added: []string{"z"}},
		},
		"redeclared-twice-in-one-message":      {{Text: "base", Added: []string{"a"}}, {Removed: []string{"a"}}, {Added: []string{"a", "a"}}},
		"redefinition-is-no-removal":           {{Text: "base", Added: []string{"a"}}, {Text: "", Added: []string{"a"}, Removed: []string{"a"}}, {Added: []string{"b"}}},
		"redefinition-beside-a-true-removal":   {{Text: "base", Added: []string{"a", "b"}}, {Added: []string{"a"}, Removed: []string{"a", "b"}}, {Added: []string{"c"}}},
		"removal-without-a-prior-declaration":  {{Text: "base", Added: []string{"a"}}, {Removed: []string{"ghost"}}, {Added: []string{"b"}}},
		"removal-in-the-leading-system":        {{Text: "base", Added: []string{"a"}, Removed: []string{"z"}}, {Added: []string{"b"}}},
		"leading-system-without-tools":         {{Text: "base"}, {Added: []string{"a"}}, {Added: []string{"b"}}},
		"empty-tool-arrays":                    {{Text: "base", Added: []string{}, Removed: []string{}}, {Added: []string{}, Removed: []string{}}},
		"names-with-punctuation-and-unicode":   {{Text: "base", Added: []string{"a,b", "x\ny", "日本", "sp ace", "é"}}, {Removed: []string{"a,b", "x\ny", "日本", "sp ace"}}, {Added: []string{"q"}}},
		"update-text-before-the-removal-block": {{Text: "base", Added: []string{"a"}}, {Text: "new rules", Removed: []string{"a"}}, {Text: "more rules", Added: []string{"b"}}},
	}
}

// toolNoticeRandomCases draws seeded transcripts over a pool of names, so the unavailable list reaches several entries and loses and regains them.
func toolNoticeRandomCases(rng *rand.Rand, count int) []string {
	pool := []string{"alpha", "beta", "gamma", "a,b", "x\ny", "日本", "with space", "é"}
	subset := func(max int) []string {
		var names []string
		for range rng.IntN(max + 1) {
			names = append(names, pool[rng.IntN(len(pool))])
		}
		return names
	}
	var contexts []string
	for range count {
		systems := []toolNoticeSystem{{Text: "base", Added: subset(3)}}
		for range 1 + rng.IntN(6) {
			text := []string{"", "update", "line1\nline2"}[rng.IntN(3)]
			systems = append(systems, toolNoticeSystem{Text: text, Added: subset(3), Removed: subset(3)})
		}
		contexts = append(contexts, string(toolNoticeContext(systems)))
	}
	return contexts
}

// Responses requests (convertResponsesMessages through the openai-responses stream path) over the tool-change transcripts above and 400 seeded ones,
// each under the transports Pi distinguishes: additional_tools, synthetic tool search, system messages without tool anchoring and no mid-conversation
// system messages, for developer and system instruction roles. The payload handed to onPayload by the pinned pi-ai and by PiG must be equal.
func TestOpenAIResponsesToolChangeNoticesMatchPi(t *testing.T) {
	type probe struct {
		Provider  string          `json:"provider"`
		BaseURL   string          `json:"baseUrl"`
		APIKey    string          `json:"apiKey"`
		Reasoning bool            `json:"reasoning"`
		Input     []string        `json:"input"`
		Compat    json.RawMessage `json:"compat,omitempty"`
		Options   map[string]any  `json:"options"`
		Context   json.RawMessage `json:"context"`
	}
	transports := []struct {
		name      string
		compat    string
		reasoning bool
	}{
		{"additional_tools", `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true}`, true},
		{"additional_tools-system-role", `{"supportsMidConvoSystemMessages":true,"supportsAdditionalTools":true}`, false},
		{"tool-search", `{"supportsMidConvoSystemMessages":true,"supportsToolSearch":true}`, true},
		{"mid-conversation-system", `{"supportsMidConvoSystemMessages":true}`, true},
		{"folded", `{}`, true},
	}
	var probes []probe
	var labels []string
	add := func(label string, context json.RawMessage) {
		for _, transport := range transports {
			probes = append(probes, probe{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "fake-key", Reasoning: transport.reasoning, Input: []string{"text"}, Compat: json.RawMessage(transport.compat), Options: map[string]any{}, Context: context})
			labels = append(labels, label+" "+transport.name)
		}
	}
	for name, systems := range toolNoticeCases() {
		add(name, toolNoticeContext(systems))
	}
	for i, context := range toolNoticeRandomCases(rand.New(rand.NewPCG(20261102, 1)), 400) {
		add(fmt.Sprintf("random %d", i), json.RawMessage(context))
	}
	want := piPayloads(t, "testdata/openai_responses_payload.mjs", probes)
	failures := 0
	classes := map[string]int{}
	var notices, stillUnavailable, multiStillUnavailable, removals, multiRemovals int
	for i, p := range probes {
		var payload struct {
			Input []struct {
				Content any `json:"content"`
			} `json:"input"`
		}
		if err := json.Unmarshal([]byte(want[i]), &payload); err != nil {
			t.Fatalf("%s: Pi payload: %v", labels[i], err)
		}
		for _, item := range payload.Input {
			text, _ := item.Content.(string)
			if strings.Contains(text, "This is an incremental tools update.") {
				notices++
			}
			if _, list, ok := strings.Cut(text, "\nStill unavailable: "); ok {
				stillUnavailable++
				if strings.Contains(list, ", ") {
					multiStillUnavailable++
				}
			}
			if _, list, ok := strings.Cut(text, "Do not call them:\n"); ok {
				removals++
				if strings.Contains(list, "\n- ") {
					multiRemovals++
				}
			}
		}
		model := &ai.Model{ID: "gpt-test", DisplayName: "gpt-test", Input: p.Input, Capabilities: ai.ModelCapabilities{ContextWindow: 400000, MaxOutputTokens: 16384}, ProviderMeta: ai.ProviderMetadata{API: ai.APIOpenAIResponses, ProviderID: p.Provider, BaseURL: p.BaseURL, Reasoning: p.Reasoning, Compat: &ai.ModelCompat{}}}
		if err := json.Unmarshal(p.Compat, model.ProviderMeta.Compat); err != nil {
			t.Fatal(err)
		}
		var got string
		options := ai.StreamOptions{APIKey: p.APIKey, OnPayload: func(payload any, _ *ai.Model) (any, error) {
			encoded, err := sdkjson.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			got = string(encoded)
			return nil, errors.New("payload captured")
		}}
		stream, err := ai.StreamOpenAIResponses(payloadContext(t), model, payloadTranscript(t, p.Context), options)
		if err == nil {
			stream.Result()
		}
		if got == "" {
			t.Fatalf("%s: PiG produced no payload", labels[i])
		}
		comparePayload(t, labels[i], got, want[i], &failures, classes)
	}
	if failures > 0 {
		t.Fatalf("%d of %d probes differ from Pi: %v", failures, len(probes), classes)
	}
	// The denominators come from Pi's payloads: a corpus that never reached a branch would pass it vacuously.
	for name, count := range map[string]int{"incremental notices": notices, "Still unavailable lines": stillUnavailable, "multi-name Still unavailable lines": multiStillUnavailable, "removal notices": removals, "multi-name removal notices": multiRemovals} {
		if count < 20 {
			t.Errorf("Pi payloads hold %d %s over %d probes, want at least 20", count, name, len(probes))
		}
	}
}
