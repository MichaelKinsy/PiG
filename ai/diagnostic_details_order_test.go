package ai

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/orderedjson"
)

// diagnosticDetailsKeys returns the member order of the marshalled details of diagnostics[index], and of the object at member nested when nested is set.
func diagnosticDetailsKeys(t *testing.T, diagnostics []AssistantMessageDiagnostic, index int, nested string) []string {
	t.Helper()
	encoded, err := json.Marshal(diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(encoded, &list); err != nil || index >= len(list) {
		t.Fatalf("diagnostics = %s (%v)", encoded, err)
	}
	diagnostic, err := orderedjson.Parse(list[index])
	if err != nil {
		t.Fatal(err)
	}
	details, ok := diagnostic.Get("details")
	if !ok {
		t.Fatalf("diagnostic %s has no details", list[index])
	}
	object, err := orderedjson.Parse(details)
	if err != nil {
		t.Fatal(err)
	}
	if nested != "" {
		member, _ := object.Get(nested)
		if object, err = orderedjson.Parse(member); err != nil {
			t.Fatalf("details.%s = %s: %v", nested, member, err)
		}
	}
	return object.Keys()
}

// pi-messages.ts:145-155 createPiMessagesResponseError builds the details literal version, provider, model, url, status, statusText, error or body, timestampMs, and `error` is the parsed body's object, which keeps the backend's member order. JSON.stringify writes them in that order; a Go map sorted them.
func TestPiMessagesResponseFailureDetailsKeepPisMemberOrder(t *testing.T) {
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{status: 401, rawBody: `{"error":{"message":"Token expired","code":"unauthorized","param":null}}`})
	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	if got, want := diagnosticDetailsKeys(t, message.Diagnostics, 0, ""), []string{"version", "provider", "model", "url", "status", "statusText", "error", "timestampMs"}; !slices.Equal(got, want) {
		t.Errorf("details keys = %v, want %v", got, want)
	}
	if got, want := diagnosticDetailsKeys(t, message.Diagnostics, 0, "error"), []string{"message", "code", "param"}; !slices.Equal(got, want) {
		t.Errorf("details.error keys = %v, want %v", got, want)
	}
}

// pi-messages.ts:169-177 appendRewriteDiagnostic: details are `{ ...rewrite }`, the parsed wire object in the order the backend wrote it.
func TestPiMessagesRewriteDetailsKeepTheBackendsMemberOrder(t *testing.T) {
	done := json.RawMessage(fmt.Sprintf(`{"type":"done","reason":"stop","usage":%s,"rewrite":{"policyId":"p","policyVersion":3,"changed":true,"tokenCountChange":-4,"messageCountChange":0,"systemPromptChanged":false,"extra":{"z":1,"a":2}}}`, mustJSON(t, piMessagesTestUsage)))
	baseURL, _ := startPiMessagesServer(t, piMessagesResponder{events: []any{done}})
	_, message := collectPiMessages(t, newTestPiMessagesProvider(baseURL, nil), StreamOptions{})

	if got, want := diagnosticDetailsKeys(t, message.Diagnostics, 0, ""), []string{"policyId", "policyVersion", "changed", "tokenCountChange", "messageCountChange", "systemPromptChanged", "extra"}; !slices.Equal(got, want) {
		t.Errorf("details keys = %v, want %v", got, want)
	}
	if got, want := diagnosticDetailsKeys(t, message.Diagnostics, 0, "extra"), []string{"z", "a"}; !slices.Equal(got, want) {
		t.Errorf("details.extra keys = %v, want %v", got, want)
	}
}

// anthropic-messages.ts:872-881 maps each input transformation to `{ type, path, reason }`; JSON.stringify keeps that literal order.
func TestAnthropicInputTransformationsDetailsKeepPisMemberOrder(t *testing.T) {
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"usage\":{\"input_tokens\":1},\"input_transformations\":[{\"reason\":\"r\",\"path\":\"p\",\"type\":\"thinking_dropped\"}]}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	result, _ := runAnthropicEvents(t, sse)
	encoded, err := json.Marshal(result.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if want := `"details":{"transformations":[{"type":"thinking_dropped","path":"p","reason":"r"}]}`; !strings.Contains(string(encoded), want) {
		t.Errorf("diagnostics = %s, want %s", encoded, want)
	}
}

// A diagnostic read from a session is written back as JSON.stringify writes the parsed object: every member in the order it was read, and an empty details object kept.
func TestAssistantMessageDiagnosticKeepsTheDetailsOrderItWasReadIn(t *testing.T) {
	for _, text := range []string{
		`{"type":"x","timestamp":1,"details":{"z":1,"a":{"y":[{"q":1,"b":2}],"c":3},"m":"<&>"}}`,
		`{"type":"x","timestamp":1,"error":{"name":"Error","message":"m","code":7},"details":{}}`,
		`{"type":"x","timestamp":1}`,
	} {
		var diagnostic AssistantMessageDiagnostic
		if err := json.Unmarshal([]byte(text), &diagnostic); err != nil {
			t.Fatal(err)
		}
		encoded, err := orderedjson.Marshal(diagnostic)
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != text {
			t.Errorf("round trip of %s = %s", text, encoded)
		}
		copied := cloneDiagnostics([]AssistantMessageDiagnostic{diagnostic})
		if again, _ := orderedjson.Marshal(copied[0]); string(again) != text {
			t.Errorf("clone of %s = %s", text, again)
		}
	}
}
