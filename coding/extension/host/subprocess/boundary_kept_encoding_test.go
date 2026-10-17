package subprocess_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// keptPreview builds the preview of inner as Session.projectBoundaryContext does without drafts: the session's own messages, each entry's record as its source.
func keptPreview(inner *icodingagent.Session) (extension.BoundaryContextPreview, []json.RawMessage) {
	projection := inner.BuildSessionProjectionView()
	entries := make([]extension.ProjectedSessionEntry, len(projection.Entries))
	sources := make([]json.RawMessage, len(projection.Entries))
	for i, projected := range projection.Entries {
		entries[i] = extension.ProjectedSessionEntry{Messages: append([]extension.AgentMessage{}, projected.Messages...)}
		sources[i] = projected.SourceEntry.Raw()
	}
	messages := append([]extension.AgentMessage{}, projection.Messages...)
	return extension.BoundaryContextPreview{ContextEntries: entries, ContextMessages: messages, LLMMessages: agent.ConvertToLLM(projection.Messages)}, sources
}

// A boundary dispatch encodes the projected part of its preview from the encodings of the messages and records the session's previous dispatch kept, plus what is new. The bytes are the bytes of encoding the whole preview, for every message shape (custom and bash messages, which have no key, a context edit, which replaces an entry's messages with new objects, a compaction, characters JSON escapes), and a pass with nothing new encodes no message again.
func TestKeptProjectionEncodesAsTheWholePreview(t *testing.T) {
	inner := icodingagent.NewSession("s", t.TempDir())
	user := func(text string, at int64) string {
		t.Helper()
		id, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: text}}, Timestamp: at}})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	answer := func(text string, at int64) string {
		t.Helper()
		id, err := inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}, ai.ToolCall{ID: "call", Name: "read", Arguments: map[string]any{"path": "<a&b>"}}}, StopReason: ai.StopReasonToolUse, Provider: "faux", ModelID: "faux-1", Timestamp: at}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inner.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "call", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "line\u2028<x>"}}, Timestamp: at + 1}}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	sessionEncodings := &boundarywire.Encodings{}
	pass := func(stage string) {
		t.Helper()
		preview, sources := keptPreview(inner)
		_, cache := boundarywire.WithSessionCache(context.Background(), sessionEncodings)
		cache.Built(preview, boundarywire.Key{Session: inner, Revision: inner.Revision()}, true, sources)
		cache.Shared()
		encodings, records, kept := cache.Kept(preview)
		if !kept {
			t.Fatalf("%s: the preview's encodings are not kept", stage)
		}
		got, err := subprocess.EncodeKeptProjectionForTest(encodings, records, preview)
		if err != nil {
			t.Fatal(err)
		}
		want, err := subprocess.MarshalProjectionForTest(sources, preview)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s: kept encoding\n got %s\nwant %s", stage, got, want)
		}
	}

	pass("empty session")
	// Absent collections encode as null, as json.Marshal writes nil slices.
	for _, preview := range []extension.BoundaryContextPreview{{}, {ContextEntries: []extension.ProjectedSessionEntry{{}}, ContextMessages: []extension.AgentMessage{}}} {
		sources := make([]json.RawMessage, len(preview.ContextEntries))
		for i := range sources {
			sources[i] = json.RawMessage(`{"type":"custom"}`)
		}
		got, err := subprocess.EncodeKeptProjectionForTest(&boundarywire.Encodings{}, sources, preview)
		if err != nil {
			t.Fatal(err)
		}
		if want, err := subprocess.MarshalProjectionForTest(sources, preview); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("kept encoding of %+v\n got %s\nwant %s (%v)", preview, got, want, err)
		}
	}
	first := user("hello <b> & \u2029", 1)
	answer("working", 2)
	if _, err := inner.AppendCustomMessageEntry("note", "custom <text>", true, map[string]any{"z": 1, "a": 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendBashExecution(icodingagent.BashExecutionMessage{Role: "bashExecution", Command: "ls", Output: "a\nb", Timestamp: 5}); err != nil {
		t.Fatal(err)
	}
	// convertToLlm sends nothing for an excluded command.
	if _, err := inner.AppendBashExecution(icodingagent.BashExecutionMessage{Role: "bashExecution", Command: "pwd", Output: "/", Timestamp: 5, ExcludeFromContext: new(true)}); err != nil {
		t.Fatal(err)
	}
	pass("first pass")
	pass("repeated pass")
	user("more", 6)
	second := answer("again", 7)
	pass("after appends")
	if _, err := inner.AppendContextEdit(first, &icodingagent.ContextEditReplacement{Content: json.RawMessage(`"edited <c>"`)}); err != nil {
		t.Fatal(err)
	}
	pass("after a context edit")
	if _, err := inner.AppendContextEdit(second, nil); err != nil {
		t.Fatal(err)
	}
	pass("after an omission")
	if _, err := inner.AppendCompaction("summary <s>", second, 100, nil, false, nil); err != nil {
		t.Fatal(err)
	}
	user("after compaction", 9)
	pass("after a compaction")

	// A pass over an unchanged history encodes only the messages that have no key (here the compaction summary): its cost does not grow with the history.
	// The race detector adds allocations of its own (CI -race counted 119 against 114), so the bound runs without it; the encodings above run in both modes.
	if subprocess.RaceEnabledForTest {
		return
	}
	passAllocs := func() float64 {
		t.Helper()
		pass("long history")
		preview, sources := keptPreview(inner)
		_, cache := boundarywire.WithSessionCache(context.Background(), sessionEncodings)
		cache.Built(preview, boundarywire.Key{Session: inner, Revision: inner.Revision()}, true, sources)
		cache.Shared()
		encodings, records, _ := cache.Kept(preview)
		return testing.AllocsPerRun(5, func() {
			if _, err := subprocess.EncodeKeptProjectionForTest(encodings, records, preview); err != nil {
				t.Fatal(err)
			}
		})
	}
	for i := range 20 {
		user("history", int64(10+i))
	}
	shorter := passAllocs()
	for i := range 100 {
		user("history", int64(30+i))
	}
	if longer := passAllocs(); longer > shorter+2 {
		t.Fatalf("a pass over 100 more kept messages costs %.0f allocations, %.0f before", longer, shorter)
	}
}
