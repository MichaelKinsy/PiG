package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
)

type fauxScriptedProbe struct {
	Content      []map[string]any `json:"content"`
	StopReason   string           `json:"stopReason,omitempty"`
	ErrorMessage string           `json:"errorMessage,omitempty"`
	Extra        map[string]any   `json:"extra"`
}

type fauxScriptedEvent struct {
	Type    string         `json:"type"`
	Message map[string]any `json:"message"`
}

type fauxScriptedResult struct {
	Events []fauxScriptedEvent `json:"events"`
	Result map[string]any      `json:"result"`
}

var fauxScriptedKeys = []string{"responseModel", "responseId", "providerThinkingLevel", "thinkingLevel", "diagnostics", "rawStopReason", "endTurn", "errorMessage", "stopReason"}

// pickFauxScripted keeps the members the probe compares, as the oracle's pick does, from a message marshalled to JSON.
func pickFauxScripted(t *testing.T, message *AssistantMessage, keys []string) map[string]any {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var all map[string]any
	if err := json.Unmarshal(raw, &all); err != nil {
		t.Fatal(err)
	}
	picked := map[string]any{}
	for _, key := range keys {
		if value, ok := all[key]; ok {
			picked[key] = value
		}
	}
	if value, ok := all["durationMs"]; ok {
		picked["durationMs"] = "measured"
		if value == 777.0 {
			picked["durationMs"] = 777.0
		}
	}
	return picked
}

func (probe fauxScriptedProbe) response(t *testing.T) FauxResponse {
	t.Helper()
	response := FauxResponse{StopReason: probe.StopReason, ErrorMessage: probe.ErrorMessage}
	one := int64(1)
	response.Timestamp = &one
	if response.StopReason == "" {
		response.StopReason = "stop"
	}
	for _, block := range probe.Content {
		switch block["type"] {
		case "text":
			response.Content = append(response.Content, FauxText(block["text"].(string)))
		case "thinking":
			response.Content = append(response.Content, FauxThinking(block["thinking"].(string)))
		default:
			response.Content = append(response.Content, FauxToolCall(block["name"].(string), block["arguments"].(map[string]any), &FauxToolCallOptions{ID: block["id"].(string)}))
		}
	}
	extra := probe.Extra
	if value, ok := extra["responseModel"].(string); ok {
		response.ResponseModel = value
	}
	if value, ok := extra["responseId"].(string); ok {
		response.ResponseID = value
	}
	if value, ok := extra["providerThinkingLevel"].(string); ok {
		response.ProviderThinkingLevel = value
	}
	if value, ok := extra["thinkingLevel"].(string); ok {
		response.ThinkingLevel = ModelThinkingLevel(value)
	}
	if value, ok := extra["rawStopReason"].(string); ok {
		response.RawStopReason = value
	}
	if value, ok := extra["durationMs"].(float64); ok {
		response.DurationMs = new(int64(value))
	}
	if value, ok := extra["endTurn"].(bool); ok {
		response.EndTurn = &value
	}
	if value, ok := extra["durationMs"].(float64); ok {
		response.DurationMs = new(int64(value))
	}
	if value, ok := extra["diagnostics"].([]any); ok {
		for _, item := range value {
			entry := item.(map[string]any)
			response.Diagnostics = append(response.Diagnostics, AssistantMessageDiagnostic{Type: entry["type"].(string), Timestamp: int64(entry["timestamp"].(float64)), Details: entry["details"].(map[string]any)})
		}
	}
	return response
}

// faux.ts:282-291 and streamWithDeltas: a scripted message keeps responseModel, responseId, providerThinkingLevel, thinkingLevel, diagnostics, rawStopReason, endTurn and durationMs
// (cloneMessage overwrites only api, provider, model, timestamp and usage), so every partial and the final message carry them, error and aborted messages included.
func TestFauxScriptedMessageMembersMatchPi(t *testing.T) {
	text := []map[string]any{{"type": "text", "text": "hello"}}
	mixed := []map[string]any{{"type": "thinking", "thinking": "hmm"}, {"type": "text", "text": "ok"}, {"type": "toolCall", "id": "call_1", "name": "read", "arguments": map[string]any{"path": "a"}}}
	diagnostics := []any{map[string]any{"type": "anthropic_input_transformations", "timestamp": 5.0, "details": map[string]any{"count": 2.0}}}
	probes := []fauxScriptedProbe{
		{Content: text, Extra: map[string]any{}},
		{Content: text, Extra: map[string]any{"responseModel": "faux-concrete", "responseId": "resp_1"}},
		{Content: mixed, Extra: map[string]any{"providerThinkingLevel": "high", "thinkingLevel": "medium", "rawStopReason": "end_turn", "endTurn": true}},
		{Content: text, Extra: map[string]any{"endTurn": false, "diagnostics": diagnostics}},
		{Content: text, StopReason: "length", Extra: map[string]any{"rawStopReason": "max_tokens", "responseModel": "m"}},
		{Content: text, StopReason: "error", ErrorMessage: "provider failed", Extra: map[string]any{"responseModel": "m", "providerThinkingLevel": "low", "diagnostics": diagnostics, "endTurn": true}},
		{Content: text, Extra: map[string]any{"durationMs": 1234.0}},
		{Content: mixed, Extra: map[string]any{"durationMs": 0.0, "responseId": "resp_d"}},
		{Content: text, StopReason: "error", ErrorMessage: "provider failed", Extra: map[string]any{"durationMs": 7.0}},
		{Content: mixed, StopReason: "aborted", ErrorMessage: "Request was aborted", Extra: map[string]any{"thinkingLevel": "high", "rawStopReason": "cancelled"}},
		{Content: text, Extra: map[string]any{"durationMs": 777.0}},
		{Content: mixed, StopReason: "error", ErrorMessage: "provider failed", Extra: map[string]any{"durationMs": 777.0}},
		{Content: mixed, StopReason: "aborted", ErrorMessage: "Request was aborted", Extra: map[string]any{"durationMs": 777.0}},
	}
	input, err := json.Marshal(probes)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", "testdata/faux_scripted_fields.mjs", pigversion.UpstreamVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []fauxScriptedResult
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	for i, probe := range probes {
		provider := NewFauxProvider(FauxConfig{API: "faux-probe", ProviderID: "faux-probe", TokenSize: &FauxTokenSize{Min: new(100), Max: new(100)}})
		provider.SetResponses([]FauxResponseStep{FauxStaticStep(probe.response(t))})
		stream, err := provider.Stream(t.Context(), TranscriptContext{}, StreamOptions{})
		if err != nil {
			t.Fatal(err)
		}
		keys := fauxScriptedKeys
		if _, preset := probe.Extra["durationMs"]; preset {
			keys = append(slices.Clone(fauxScriptedKeys), "durationMs")
		}
		var got fauxScriptedResult
		for event := range stream.Events(context.Background()) {
			var message *AssistantMessage
			switch event := event.(type) {
			case StartEvent:
				message = event.Partial
			case TextStartEvent:
				message = event.Partial
			case TextDeltaEvent:
				message = event.Partial
			case TextEndEvent:
				message = event.Partial
			case ThinkingStartEvent:
				message = event.Partial
			case ThinkingDeltaEvent:
				message = event.Partial
			case ThinkingEndEvent:
				message = event.Partial
			case ToolCallStartEvent:
				message = event.Partial
			case ToolCallDeltaEvent:
				message = event.Partial
			case ToolCallEndEvent:
				message = event.Partial
			case DoneEvent:
				message = event.Message
			case ErrorEvent:
				message = event.Error
			}
			got.Events = append(got.Events, fauxScriptedEvent{Type: string(event.EventType()), Message: pickFauxScripted(t, message, keys)})
		}
		got.Result = pickFauxScripted(t, stream.Result(), keys)
		if !reflect.DeepEqual(got, want[i]) {
			gotJSON, _ := json.MarshalIndent(got, "", " ")
			wantJSON, _ := json.MarshalIndent(want[i], "", " ")
			t.Errorf("probe %d (%+v): members differ from Pi\n got  %s\n want %s", i, probe.Extra, gotJSON, wantJSON)
		}
	}
}
