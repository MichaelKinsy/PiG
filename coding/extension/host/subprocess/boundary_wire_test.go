package subprocess

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// boundaryPreview builds a fresh preview, as each build does, whose contents depend only on text.
func boundaryPreview(text string, pending bool) extension.BoundaryContextPreview {
	call := ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "<a&b>.go", "n": 2.5}}
	messages := []agent.AgentMessage{
		{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text + " \u2028 é " + jsstring.FromUTF16([]uint16{0xd800})}}, Timestamp: 1}},
		{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "plan"}, call}, StopReason: ai.StopReasonToolUse, Usage: &ai.Usage{Input: 3, Output: 4, TotalTokens: 7}, Provider: "faux", ModelID: "faux-1", Timestamp: 2}},
		{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "<html>&"}}, Timestamp: 3}},
	}
	entries := make([]extension.ProjectedSessionEntry, len(messages))
	for i := range messages {
		entries[i] = extension.ProjectedSessionEntry{SourceEntry: map[string]any{"type": "message", "id": string(rune('a' + i)), "parentId": nil, "z": []any{1.0, "x"}, "a": map[string]any{}}, Messages: []agent.AgentMessage{messages[i]}}
	}
	preview := extension.BoundaryContextPreview{ContextEntries: entries, ContextMessages: append([]agent.AgentMessage(nil), messages...), LLMMessages: agent.ConvertToLLM(messages), PendingMessages: []agent.AgentMessage{}}
	if pending {
		preview.PendingMessages = []agent.AgentMessage{{User: &agent.UserMessage{Role: "user", Content: ai.UserText("queued"), Timestamp: 9}}}
		preview.CanContinue = true
	}
	return preview
}

func boundaryEvents(preview extension.BoundaryContextPreview) []any {
	state := extension.BoundaryState{Entries: []extension.SessionBoundaryDraft{{Type: "custom", CustomType: "note", Data: map[string]any{"k": "<v>"}}}, Continue: true, Context: preview, Outcome: extension.AgentActivityCompleted}
	message := agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, StopReason: ai.StopReasonStop, Timestamp: 4}}
	return []any{
		extension.TurnEndEvent{BoundaryState: &state, Type: "turn_end", TurnIndex: 2, Message: message, ToolResults: []extension.ToolResultMessage{}, MessageEntryID: "m", ToolResultEntryIds: []string{}},
		&extension.AgentBeforeSettleEvent{Type: "agent_before_settle", BoundaryState: state},
	}
}

func marshalEvent(t *testing.T, event any) string {
	t.Helper()
	wire, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}

// Each handler of a boundary dispatch receives the JSON of its own event (runner.ts emitBoundary). Sharing the projected part between handlers must leave every handler's bytes those of encoding its event directly.
func TestBoundaryEventWireEncodesAsTheEvent(t *testing.T) {
	key := boundarywire.Key{Session: t, Revision: 1}
	for _, pending := range []bool{false, true} {
		for j, event := range boundaryEvents(boundaryPreview("alone", pending)) {
			want := marshalEvent(t, event)
			if got := marshalEvent(t, boundaryEventWire(event, nil)); got != want {
				t.Fatalf("event %d without a cache:\n got %s\nwant %s", j, got, want)
			}
			_, cache := boundarywire.WithCache(context.Background())
			if got := marshalEvent(t, boundaryEventWire(event, cache)); got != want {
				t.Fatalf("event %d with an empty cache:\n got %s\nwant %s", j, got, want)
			}
		}
	}
	// One dispatch: build after build, as EmitBoundary calls buildContext after each handler.
	for j := range 2 {
		_, cache := boundarywire.WithCache(context.Background())
		var shared int
		for i, build := range []struct {
			text    string
			key     boundarywire.Key
			pending bool
			viaWire bool
		}{
			{"first", key, false, true},
			{"first", key, true, true},
			{"first", key, false, true},
			// A Go handler receives this build and changes what the next build reads without a new revision.
			{"second", boundarywire.Key{Session: t, Revision: 2}, false, false},
			{"third", boundarywire.Key{Session: t, Revision: 2}, false, true},
			{"third", boundarywire.Key{Session: t, Revision: 2}, false, true},
			{"fourth", boundarywire.Key{Session: t, Revision: 3}, false, true},
		} {
			preview := boundaryPreview(build.text, build.pending)
			cache.Built(preview, build.key, true, nil)
			if _, hit := cache.Encoded(preview); hit {
				shared++
			}
			if build.viaWire {
				cache.HandlerStarted()
			}
			event := boundaryEvents(preview)[j]
			if got, want := marshalEvent(t, boundaryEventWire(event, cache)), marshalEvent(t, event); got != want {
				t.Fatalf("build %d event %d:\n got %s\nwant %s", i, j, got, want)
			}
		}
		// Builds 1 and 2 share build 0's encoding; build 5 shares build 4's. Build 4 follows the Go handler and shares nothing.
		if shared != 3 {
			t.Fatalf("event %d: %d builds reused an encoding, want 3", j, shared)
		}
	}
}

// A shared encoding is only ever the latest preview's: a preview from changed inputs, an uncacheable build, or a build after a handler that was not a subprocess handler is encoded afresh.
func TestBoundaryWireCacheSharesOnlyAcrossUnchangedInputs(t *testing.T) {
	key := boundarywire.Key{Session: t, Revision: 1}
	encode := func(cache *boundarywire.Cache, preview extension.BoundaryContextPreview) string {
		t.Helper()
		return marshalEvent(t, boundaryContextWire{cache, preview})
	}
	for name, step := range map[string]func(*boundarywire.Cache, extension.BoundaryContextPreview){
		"new revision": func(c *boundarywire.Cache, p extension.BoundaryContextPreview) {
			c.HandlerStarted()
			c.Built(p, boundarywire.Key{Session: t, Revision: 2}, true, nil)
		},
		"drafts": func(c *boundarywire.Cache, p extension.BoundaryContextPreview) {
			c.HandlerStarted()
			c.Built(p, key, false, nil)
		},
		"a Go handler ran": func(c *boundarywire.Cache, p extension.BoundaryContextPreview) {
			c.HandlerStarted()
			c.Built(boundaryPreview("before", false), key, true, nil)
			// The handler that receives that build is a Go handler: no HandlerStarted.
			c.Built(p, key, true, nil)
		},
		"not the latest build": func(c *boundarywire.Cache, p extension.BoundaryContextPreview) {
			c.HandlerStarted()
			c.Built(boundaryPreview("latest", false), key, true, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, cache := boundarywire.WithCache(context.Background())
			first := boundaryPreview("before", false)
			cache.Built(first, key, true, nil)
			cache.HandlerStarted()
			_ = encode(cache, first)
			changed := boundaryPreview("after", false)
			step(cache, changed)
			if got := encode(cache, changed); !strings.Contains(got, "after") || strings.Contains(got, "before") {
				t.Fatalf("the changed preview encodes as %s", got)
			}
		})
	}
	// The control: same inputs after a subprocess handler share the first encoding.
	_, cache := boundarywire.WithCache(context.Background())
	first := boundaryPreview("before", false)
	cache.Built(first, key, true, nil)
	cache.HandlerStarted()
	want := encode(cache, first)
	second := boundaryPreview("before", false)
	cache.Built(second, key, true, nil)
	if wire, hit := cache.Encoded(second); !hit || !bytes.Contains([]byte(want), wire[:len(wire)-1]) {
		t.Fatalf("the unchanged preview did not share the first encoding (hit %v)", hit)
	}
}

// Pi hands a boundary handler the session's entry objects (session-manager.ts buildSessionProjection), whose members are in the record's order. A preview built with its records encodes each source as its record, until a Go handler receives the preview: its decoded values are then what the handler may have changed, so they are encoded instead and nothing is shared.
func TestBoundaryWireEncodesEachSourceAsItsRecord(t *testing.T) {
	records := []json.RawMessage{
		json.RawMessage(`{"type":"message","id":"a","parentId":null,"timestamp":"t","message":{"role":"user","content":"x \ud800","timestamp":1.0}}`),
		json.RawMessage(`{"type":"model_change","id":"b","parentId":"a","timestamp":"t","provider":"p","modelId":"m"}`),
		json.RawMessage(`{"type":"custom","id":"c","parentId":"b","timestamp":"t","customType":"s","data":{"z":1,"a":[1e2,0.5]}}`),
	}
	lazy := func() extension.BoundaryContextPreview {
		preview := boundaryPreview("records", false)
		for i := range preview.ContextEntries {
			preview.ContextEntries[i].SourceEntry = nil
		}
		return preview
	}
	key := boundarywire.Key{Session: t, Revision: 1}
	_, cache := boundarywire.WithCache(context.Background())
	preview := lazy()
	cache.Built(preview, key, true, records)
	cache.HandlerStarted()
	got := marshalEvent(t, boundaryContextWire{cache, preview})
	want := `"contextEntries":[{"sourceEntry":` + string(records[0]) + `,"messages":`
	if !strings.Contains(got, want) {
		t.Fatalf("the first entry does not encode as its record:\n got %s\nwant it to contain %s", got, want)
	}
	for _, record := range records {
		if !strings.Contains(got, `{"sourceEntry":`+string(record)+`,`) {
			t.Fatalf("record %s is not in %s", record, got)
		}
	}
	// The same encoding with each source decoded, members sorted, is the rest of the preview unchanged.
	decoded := lazy()
	for i, record := range records {
		if err := json.Unmarshal(record, &decoded.ContextEntries[i].SourceEntry); err != nil {
			t.Fatal(err)
		}
	}
	_, empty := boundarywire.WithCache(context.Background())
	mapped := marshalEvent(t, boundaryContextWire{empty, decoded})
	sorted := got
	for i, record := range records {
		value, err := json.Marshal(decoded.ContextEntries[i].SourceEntry)
		if err != nil {
			t.Fatal(err)
		}
		sorted = strings.Replace(sorted, string(record), string(value), 1)
	}
	if mapped != sorted || mapped == got {
		t.Fatalf("the decoded preview encodes as\n%s\nwant the record encoding with each source's members sorted\n%s", mapped, sorted)
	}

	// A Go handler receives the next build from the same inputs: Deliver decodes its sources in place, and it neither shares nor stores an encoding.
	next := lazy()
	cache.Built(next, key, true, records)
	if err := cache.Deliver(next); err != nil {
		t.Fatal(err)
	}
	if source, ok := next.ContextEntries[2].SourceEntry.(map[string]any); !ok || source["customType"] != "s" {
		t.Fatalf("Deliver left source %#v", next.ContextEntries[2].SourceEntry)
	}
	if _, hit := cache.Encoded(next); hit {
		t.Fatal("a preview a Go handler received shared an encoding")
	}
	if _, recorded := cache.Sources(next); recorded {
		t.Fatal("a preview a Go handler received still encodes its records")
	}
	next.ContextEntries[2].SourceEntry.(map[string]any)["customType"] = "changed"
	if got := marshalEvent(t, boundaryContextWire{cache, next}); !strings.Contains(got, `"customType":"changed"`) {
		t.Fatalf("the encoding after a Go handler changed its source is %s", got)
	}
	// The build after the Go handler is encoded from its records again, afresh.
	after := lazy()
	cache.Built(after, key, true, records)
	cache.HandlerStarted()
	if _, hit := cache.Encoded(after); hit {
		t.Fatal("the build after a Go handler shared an encoding")
	}
	if got := marshalEvent(t, boundaryContextWire{cache, after}); !strings.Contains(got, string(records[2])) {
		t.Fatalf("the build after the Go handler encodes as %s", got)
	}
}
