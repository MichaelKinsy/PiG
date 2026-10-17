// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package driver

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
)

// The core compares tool declarations as JSON text (turn.DeclarationsEqual, prompt.ts declarationsEqual), so the key order of a
// schema the author declared in JSON must reach it; a schema authored as a Go map has the deterministic sorted order.
func TestParametersJSONKeepsTheDeclaredOrder(t *testing.T) {
	var schema ai.ToolSchema
	if err := json.Unmarshal([]byte(`{"name":"lookup","description":"d","parameters":{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}}`), &schema); err != nil {
		t.Fatal(err)
	}
	got := string(parametersJSON(&durable.ToolRegistration{ToolSchema: schema}))
	if want := `{"type":"object","required":["n"],"properties":{"n":{"type":"number"}}}`; got != want {
		t.Fatalf("parameters = %s, want %s", got, want)
	}
	authored := ai.ToolSchema{Name: "lookup", Parameters: map[string]any{"type": "object", "required": []any{"n"}}}
	if got, want := string(parametersJSON(&durable.ToolRegistration{ToolSchema: authored})), `{"required":["n"],"type":"object"}`; got != want {
		t.Fatalf("authored parameters = %s, want %s", got, want)
	}
}

// pi-ai's event stream stamps durationMs on the final message of a done or error event (utils/event-stream.ts): the Go ai
// message carries it as DurationMs, which the host sets from its own clock.
func TestWithDurationStampsTheFinalMessageOnly(t *testing.T) {
	message := &ai.AssistantMessage{Timestamp: 1000}
	done, _ := json.Marshal(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message})
	got := string(withDuration(done, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}, 900, 5.25, 355.5))
	var parsed struct {
		Message struct {
			DurationMs *float64 `json:"durationMs"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil || parsed.Message.DurationMs == nil || *parsed.Message.DurationMs != 350 {
		t.Fatalf("done event %s (%v)", got, err)
	}
	// An event that is not final, a message that predates the stream, and a negative span are left alone or clamped to zero.
	start, _ := json.Marshal(ai.StartEvent{Partial: message})
	if !bytes.Equal(withDuration(start, ai.StartEvent{Partial: message}, 900, 0, 350), start) {
		t.Fatal("a start event must not change")
	}
	if !bytes.Equal(withDuration(done, ai.DoneEvent{Message: message}, 2000, 0, 3000), done) {
		t.Fatal("a message older than the stream must not change")
	}
	if got := string(withDuration(done, ai.DoneEvent{Message: message}, 900, 800, 700)); !json.Valid([]byte(got)) || !strings.HasSuffix(got, `,"durationMs":0}}`) {
		t.Fatalf("negative span: %s", got)
	}
}

// The core reads a scan's filters by their wire names; a Go query struct marshalled as-is would send "OwnerTaskId", the core would
// read no filter, and a scan for a task's child conversation would return the root conversation.
func TestScanQueriesCarryTheWireNames(t *testing.T) {
	task := durable.TaskId(9)
	conv := durable.ConversationId(2)
	got, _ := json.Marshal(wireConversationQuery(durable.ConversationQuery{OwnerTaskId: &task}))
	if string(got) != `{"ownerTaskId":9}` {
		t.Fatalf("conversation query = %s", got)
	}
	kind := "pi.tool"
	got, _ = json.Marshal(wireTaskQuery(durable.TaskQuery{ConversationId: &conv, Kind: &kind}))
	if string(got) != `{"conversationId":2,"kind":"pi.tool"}` {
		t.Fatalf("task query = %s", got)
	}
	// Every filter of both queries, so a dropped field fails here and not only in the scenario that happens to use it.
	got, _ = json.Marshal(wireConversationQuery(durable.ConversationQuery{OwnerConversationId: &conv, OwnerTaskId: &task}))
	if string(got) != `{"ownerConversationId":2,"ownerTaskId":9}` {
		t.Fatalf("conversation query, every filter = %s", got)
	}
	status, abort, background := durable.TaskStatus("waiting"), true, false
	got, _ = json.Marshal(wireTaskQuery(durable.TaskQuery{ConversationId: &conv, Kind: &kind, Status: &status, AbortRequested: &abort, Background: &background}))
	if string(got) != `{"abortRequested":true,"background":false,"conversationId":2,"kind":"pi.tool","status":"waiting"}` {
		t.Fatalf("task query, every filter = %s", got)
	}
	got, _ = json.Marshal(wireTaskQuery(durable.TaskQuery{}))
	if string(got) != `{}` {
		t.Fatalf("task query, no filter = %s", got)
	}
}
