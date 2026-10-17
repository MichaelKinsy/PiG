package coding

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/boundarywire"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// agent-session.ts _buildBoundaryContext projects SessionManager.inMemory(cwd, undefined, [header, ...getBranch()]) with the drafts applied. Without drafts that preview holds the branch with its ids and parent links, so the session's own projection must be the one the copy produces, for every shape a branch can take.
func TestBoundaryProjectionWithoutDraftsIsTheCopiedPreviewProjection(t *testing.T) {
	for name, build := range map[string]func(*testing.T) *icodingagent.Session{
		"empty":           func(*testing.T) *icodingagent.Session { return icodingagent.NewSession("s", t.TempDir()) },
		"conversation":    boundaryConversation,
		"forked":          boundaryForked,
		"leaf moved back": boundaryLeafMovedBack,
		"leaf unset":      boundaryLeafUnset,
		"loaded file":     boundaryLoadedFile,
	} {
		t.Run(name, func(t *testing.T) {
			inner := build(t)
			s := &Session{inner: inner}
			got, err := s.boundaryProjection(nil)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := s.createBoundaryPreviewManager(nil)
			if err != nil {
				t.Fatal(err)
			}
			want := preview.BuildSessionProjection()
			if name != "empty" && name != "leaf unset" && len(want.Entries) == 0 {
				t.Fatal("the case projects no entries, so it compares nothing")
			}
			if g, w := boundaryProjectionWire(t, got), boundaryProjectionWire(t, want); g != w {
				t.Fatalf("projection differs from the copied preview's\n got: %s\nwant: %s", g, w)
			}
		})
	}
}

// Drafts still go through a preview, and the session itself never sees them.
func TestBoundaryProjectionWithDraftsLeavesTheSessionAlone(t *testing.T) {
	inner := boundaryConversation(t)
	s := &Session{inner: inner}
	before := len(inner.GetEntries())
	got, err := s.boundaryProjection([]extension.SessionBoundaryDraft{{Type: "custom_message", CustomType: "note", Content: "drafted", Display: true}})
	if err != nil {
		t.Fatal(err)
	}
	if after := len(inner.GetEntries()); after != before {
		t.Fatalf("the session has %d entries after a preview, want %d", after, before)
	}
	last := got.Messages[len(got.Messages)-1]
	if wire, _ := json.Marshal(last); !strings.Contains(string(wire), "drafted") {
		t.Fatalf("the preview's last message is %s, want the drafted note", wire)
	}
}

// boundaryProjectionWire is what buildBoundaryContext hands extensions from a projection: each source entry as the object its record parses to, its messages, and the context messages.
func boundaryProjectionWire(t *testing.T, projection icodingagent.SessionProjection) string {
	t.Helper()
	type entry struct {
		Source   any                  `json:"sourceEntry"`
		Messages []agent.AgentMessage `json:"messages"`
	}
	entries := make([]entry, len(projection.Entries))
	for i, projected := range projection.Entries {
		if err := json.Unmarshal(projected.SourceEntry.Raw(), &entries[i].Source); err != nil {
			t.Fatal(err)
		}
		entries[i].Messages = projected.Messages
	}
	wire, err := jsstring.MarshalJSON(map[string]any{"entries": entries, "messages": projection.Messages, "thinkingLevel": projection.ThinkingLevel, "model": projection.Model})
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}

func boundaryConversation(t *testing.T) *icodingagent.Session {
	t.Helper()
	inner := icodingagent.NewSession("s", t.TempDir())
	must := func(_ string, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	usage := &ai.Usage{Input: 10, Output: 5, TotalTokens: 15}
	must(inner.AppendModelChange("faux", "faux-1"))
	must(inner.AppendThinkingLevelChange("high"))
	must(inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "read it"}}, Timestamp: 1}}))
	must(inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.ThinkingContent{Thinking: "plan"}, ai.ToolCall{ID: "c1", Name: "read", Arguments: ai.JsonObject{"path": "a.go"}}}, StopReason: ai.StopReasonToolUse, Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: 2}}))
	toolResult, err := inner.AppendMessage(agent.AgentMessage{ToolResult: &agent.ToolResultMessage{Role: "toolResult", ToolCallID: "c1", ToolName: "read", Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "package a"}}, Details: json.RawMessage(`{"lines":1}`), Timestamp: 3}})
	must(toolResult, err)
	must(inner.AppendCustomEntry("state", map[string]any{"n": 1, "z": []any{"b", 2.5}}))
	note, err := inner.AppendCustomMessageEntry("note", "remember "+jsstring.FromUTF16([]uint16{0xd800}), true, map[string]any{"k": "v"})
	must(note, err)
	must(inner.AppendLabelChange(toolResult, new("checkpoint")))
	must(inner.AppendContextEdit(note, &icodingagent.ContextEditReplacement{Content: json.RawMessage(`"edited note"`)}))
	must(inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "done"}}, StopReason: ai.StopReasonStop, Usage: usage, Provider: "faux", ModelID: "faux-1", Timestamp: 4}}))
	if _, err := applyBoundaryDrafts(inner, []extension.SessionBoundaryDraft{{Type: "compaction", Summary: "summary of the start", FirstKeptEntryID: &toolResult}}); err != nil {
		t.Fatal(err)
	}
	must(inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "after compaction"}}, Timestamp: 5}}))
	return inner
}

func boundaryForked(t *testing.T) *icodingagent.Session {
	t.Helper()
	inner := boundaryConversation(t)
	entries := inner.GetEntries()
	if err := inner.Branch(entries[3].Base().ID); err != nil {
		t.Fatal(err)
	}
	if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "a different path"}}, Timestamp: 6}}); err != nil {
		t.Fatal(err)
	}
	return inner
}

func boundaryLeafMovedBack(t *testing.T) *icodingagent.Session {
	t.Helper()
	inner := boundaryConversation(t)
	id := inner.GetEntries()[5].Base().ID
	if err := inner.SetLeafID(&id); err != nil {
		t.Fatal(err)
	}
	return inner
}

func boundaryLeafUnset(t *testing.T) *icodingagent.Session {
	t.Helper()
	inner := boundaryConversation(t)
	if err := inner.SetLeafID(nil); err != nil {
		t.Fatal(err)
	}
	return inner
}

// A file's records keep their own member order, spacing, number spelling and members PiG does not model, and entries outside the context may be read back from the file.
func boundaryLoadedFile(t *testing.T) *icodingagent.Session {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	lines := []string{
		`{"type":"session","version":3,"id":"loaded","timestamp":"2025-01-01T00:00:00.000Z","cwd":"/tmp"}`,
		`{"type":"message","id":"e1","parentId":null,"timestamp":"2025-01-01T00:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"first \u00e9 question"}],"timestamp":1.0}, "extra":{"kept":true}}`,
		`{"parentId":"e1","type":"message","id":"e2","timestamp":"2025-01-01T00:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"first answer"}],"usage":{"input":100,"output":80,"cacheRead":0,"cacheWrite":0,"totalTokens":180,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","provider":"faux","model":"faux-1","timestamp":2}}`,
		`{"type":"custom","id":"e3","parentId":"e2","timestamp":"2025-01-01T00:00:03.000Z","customType":"state","data":{"z":1,"a":[1e2,0.5]}}`,
		`{"type":"compaction","id":"e4","parentId":"e3","timestamp":"2025-01-01T00:00:04.000Z","summary":"they talked","firstKeptEntryId":"e2","tokensBefore":180,"unmodeled":"yes"}`,
		`{"type":"message","id":"e5","parentId":"e4","timestamp":"2025-01-01T00:00:05.000Z","message":{"role":"user","content":"second question","timestamp":5}}`,
		`{"type":"message","id":"e6","parentId":"e5","timestamp":"2025-01-01T00:00:06.000Z","message":{"role":"assistant","content":[{"type":"text","text":"second answer"}],"usage":{"input":200,"output":20,"cacheRead":0,"cacheWrite":0,"totalTokens":220,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}},"stopReason":"stop","provider":"faux","model":"faux-1","timestamp":6}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	inner, err := icodingagent.NewSessionManager(dir).Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(inner.GetEntries()); got != len(lines)-1 {
		t.Fatalf("loaded %d entries, want %d", got, len(lines)-1)
	}
	return inner
}

var boundaryProjectionSink icodingagent.SessionProjection

// Without drafts the projection reads the session in place: the copy through a preview manager encodes and decodes every entry of the branch again, which made each turn_end handler of a long session pay for the whole history.
func TestBoundaryProjectionWithoutDraftsDoesNotCopyTheBranch(t *testing.T) {
	inner := boundaryConversation(t)
	for i := range 100 {
		if _, err := inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "more"}}, Timestamp: int64(10 + i)}}); err != nil {
			t.Fatal(err)
		}
	}
	s := &Session{inner: inner}
	inPlace := testing.AllocsPerRun(5, func() {
		boundaryProjectionSink, _ = s.boundaryProjection(nil)
	})
	copied := testing.AllocsPerRun(5, func() {
		preview, err := s.createBoundaryPreviewManager(nil)
		if err != nil {
			t.Fatal(err)
		}
		boundaryProjectionSink = preview.BuildSessionProjection()
	})
	t.Logf("in place %.0f allocations, through a copy %.0f", inPlace, copied)
	if inPlace*2 > copied {
		t.Fatalf("the projection without drafts costs %.0f allocations, the copied preview %.0f: it still copies the branch", inPlace, copied)
	}
}

// A dispatch shares a preview's encoding only while the inputs of its projected part stay the same: the session log and its revision, the model the history is normalized for, and no drafts.
func TestRecordedBoundaryContextKeysTheProjectedInputs(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}, Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	_, wire := boundarywire.WithCache(t.Context())
	build := func(drafts []extension.SessionBoundaryDraft) extension.BoundaryContextPreview {
		t.Helper()
		preview, err := sess.recordedBoundaryContext(wire, drafts, icodingagent.EventTurnEnd)
		if err != nil {
			t.Fatal(err)
		}
		return preview
	}
	// encoded builds a preview after a subprocess handler and reports whether it shares the encoding stored for the build before; it stores its own.
	encoded := func(drafts []extension.SessionBoundaryDraft) bool {
		t.Helper()
		wire.HandlerStarted()
		preview := build(drafts)
		_, hit := wire.Encoded(preview)
		wire.Store(preview, []byte(`{"contextEntries":[]}`))
		return hit
	}
	encoded(nil)
	if !encoded(nil) {
		t.Fatal("an unchanged session did not share the encoding")
	}
	if _, err := sess.inner.AppendCustomEntry("state", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if encoded(nil) {
		t.Fatal("an append shared the encoding of the history before it")
	}
	if !encoded(nil) {
		t.Fatal("the build after the append did not share its encoding")
	}
	var first string
	for _, entry := range sess.inner.GetEntries() {
		if entry.Base().Type == "message" {
			first = entry.Base().ID
			break
		}
	}
	if err := sess.inner.SetLeafID(&first); err != nil {
		t.Fatal(err)
	}
	if encoded(nil) {
		t.Fatal("a moved leaf shared the encoding of the other branch")
	}
	other := *fakeModel()
	other.ID = "other"
	if !encoded(nil) {
		t.Fatal("the build after the leaf move did not share its encoding")
	}
	revision := sess.inner.Revision()
	sess.agent.SetModel(&other)
	if sess.inner.Revision() != revision {
		t.Fatal("setting the agent's model changed the session log, so the model is not what this step tests")
	}
	if !encoded(nil) {
		t.Fatal("a new model did not share the encoding: llmMessages is convertToLlm of the projection (agent-session.ts:1005), which does not depend on the model")
	}
	if encoded([]extension.SessionBoundaryDraft{{Type: "custom", CustomType: "note", Data: map[string]any{}}}) {
		t.Fatal("a build with drafts shared an encoding")
	}
	// The drafted preview's encoding is not kept; the one before it was built from the same inputs as the next build.
	if !encoded(nil) {
		t.Fatal("the build after one with drafts did not share the encoding from before the drafts")
	}
}

// Each handler of a dispatch receives a freshly built preview (runner.ts emitBoundary), and the dispatch checks canContinue on one more (agent-session.ts _dispatchTurnEndBoundary). A build from the same session state as the latest one, after only subprocess handlers or none, reuses its projected part: it must equal a fresh projection, and only the parts read from the run state (pendingMessages, canContinue) are built again.
func TestRecordedBoundaryContextReusesTheProjectionOfUnchangedInputs(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	for i := range 40 {
		if _, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("message %d", i)}}, Timestamp: int64(i + 1)}}); err != nil {
			t.Fatal(err)
		}
	}
	// The history ends with an answer, so the preview can continue only once a message is queued.
	if _, err := sess.inner.AppendMessage(agent.AgentMessage{Assistant: &agent.AssistantMessage{Role: "assistant", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "answer"}}, StopReason: ai.StopReasonStop, Provider: "faux", ModelID: "faux-1", Timestamp: 50}}); err != nil {
		t.Fatal(err)
	}
	_, wire := boundarywire.WithCache(t.Context())
	build := func(drafts []extension.SessionBoundaryDraft) extension.BoundaryContextPreview {
		t.Helper()
		preview, err := sess.recordedBoundaryContext(wire, drafts, icodingagent.EventTurnEnd)
		if err != nil {
			t.Fatal(err)
		}
		return preview
	}
	// fresh is the preview a projection of the current state gives, with each source decoded as a Go handler receives it.
	fresh := func() string {
		t.Helper()
		_, own := boundarywire.WithCache(t.Context())
		preview, err := sess.recordedBoundaryContext(own, nil, icodingagent.EventTurnEnd)
		if err != nil {
			t.Fatal(err)
		}
		if err := own.Deliver(preview); err != nil {
			t.Fatal(err)
		}
		return marshalPreview(t, preview)
	}
	reused := func(before, after extension.BoundaryContextPreview) bool {
		return &before.ContextEntries[0] == &after.ContextEntries[0] && &before.ContextMessages[0] == &after.ContextMessages[0] && &before.LLMMessages[0] == &after.LLMMessages[0]
	}
	// No handler has run since the first build, as for the check after a dispatch's last handler: no Go code held its values.
	first := build(nil)
	if again := build(nil); !reused(first, again) {
		t.Fatal("a build with no handler since the last one projected an unchanged session again")
	}
	wire.HandlerStarted()
	second := build(nil)
	if !reused(first, second) {
		t.Fatal("a build after a subprocess handler projected an unchanged session again")
	}
	sess.agent.FollowUp(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserText("queued"), Timestamp: 99}})
	wire.HandlerStarted()
	third := build(nil)
	if !reused(second, third) || len(third.PendingMessages) != 1 || third.CanContinue == second.CanContinue {
		t.Fatalf("the reused preview did not read the run state again: pending %d, canContinue %v then %v", len(third.PendingMessages), second.CanContinue, third.CanContinue)
	}
	if err := wire.Deliver(third); err != nil {
		t.Fatal(err)
	}
	if got, want := marshalPreview(t, third), fresh(); got != want {
		t.Fatalf("the reused preview differs from a fresh one:\n got %s\nwant %s", got, want)
	}
	// A Go handler received the third build. Even when a subprocess handler runs after it on the same preview (the build in between failed), the next build projects again.
	wire.HandlerStarted()
	fourth := build(nil)
	if reused(third, fourth) {
		t.Fatal("a build after a Go handler reused the projection that handler held")
	}
	wire.HandlerStarted()
	if _, err := sess.inner.AppendCustomEntry("state", map[string]any{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if fifth := build(nil); reused(fourth, fifth) {
		t.Fatal("a build after an append reused the projection of the history before it")
	}
	wire.HandlerStarted()
	drafted := build([]extension.SessionBoundaryDraft{{Type: "custom", CustomType: "note", Data: map[string]any{}}})
	wire.HandlerStarted()
	sixth := build(nil)
	if reused(drafted, sixth) || len(sixth.ContextEntries) == len(drafted.ContextEntries) {
		t.Fatal("a build without drafts reused the projection of a drafted build")
	}
	// The reuse costs no projection: its allocations do not grow with the history.
	wire.HandlerStarted()
	build(nil)
	allocs := testing.AllocsPerRun(10, func() {
		wire.HandlerStarted()
		build(nil)
	})
	if entries := len(sess.inner.GetEntries()); allocs >= float64(entries) {
		t.Fatalf("a reused build costs %.0f allocations with %d entries", allocs, entries)
	}
}

func marshalPreview(t *testing.T, preview extension.BoundaryContextPreview) string {
	t.Helper()
	wire, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}

// A preview without drafts holds the session's own messages, as Pi's handlers receive them, so subprocess handlers read them without a copy. A handler that reads Go values receives copies (boundarywire.Cache.Deliver): an entry's messages and the context messages stay the same objects, llmMessages are converted from the copies, and a change to any of them leaves the session as it was.
func TestBoundaryPreviewHandsGoHandlersCopiesOfTheSessionsMessages(t *testing.T) {
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	for i := range 3 {
		if _, err := sess.inner.AppendMessage(agent.AgentMessage{User: &agent.UserMessage{Role: "user", Content: ai.UserContentBlocks{ai.TextContent{Text: fmt.Sprintf("message %d", i)}}, Timestamp: int64(i + 1)}}); err != nil {
			t.Fatal(err)
		}
	}
	_, wire := boundarywire.WithCache(t.Context())
	preview, err := sess.recordedBoundaryContext(wire, nil, icodingagent.EventTurnEnd)
	if err != nil {
		t.Fatal(err)
	}
	own := sess.inner.BuildSessionProjectionView().Messages
	entryMessages := func() []agent.AgentMessage {
		var all []agent.AgentMessage
		for _, entry := range preview.ContextEntries {
			all = append(all, entry.Messages...)
		}
		return all
	}
	if len(own) != 3 || len(preview.ContextMessages) != 3 || len(entryMessages()) != 3 {
		t.Fatalf("messages: session %d, preview %d, entries %d; want 3", len(own), len(preview.ContextMessages), len(entryMessages()))
	}
	if preview.ContextMessages[0].User != own[0].User || entryMessages()[0].User != own[0].User {
		t.Fatal("the preview copied the session's messages for subprocess handlers")
	}
	// Delivery decodes each source too; the messages encode as before.
	messagesOf := func() string {
		return marshalPreview(t, extension.BoundaryContextPreview{ContextEntries: []extension.ProjectedSessionEntry{{Messages: entryMessages()}}, ContextMessages: preview.ContextMessages, LLMMessages: preview.LLMMessages})
	}
	before := messagesOf()
	if err := wire.Deliver(preview); err != nil {
		t.Fatal(err)
	}
	if got := messagesOf(); got != before {
		t.Fatalf("delivering changed the preview's messages:\n got %s\nwant %s", got, before)
	}
	for i, message := range preview.ContextMessages {
		if message.User == own[i].User {
			t.Fatalf("context message %d is the session's own after delivery", i)
		}
		if entryMessages()[i].User != message.User {
			t.Fatalf("the entries' message %d is not context message %d", i, i)
		}
	}
	preview.ContextMessages[0].User.Content = ai.UserContentBlocks{ai.TextContent{Text: "changed"}}
	llm := preview.LLMMessages[1].(ai.UserMessage)
	llm.Content.(ai.UserContentBlocks)[0] = ai.TextContent{Text: "changed"}
	after := sess.inner.BuildSessionProjectionView().Messages
	for i, message := range after {
		if text := message.User.Content.(ai.UserContentBlocks)[0].(ai.TextContent).Text; text != fmt.Sprintf("message %d", i) {
			t.Fatalf("a Go handler's change reached the session: message %d is %q", i, text)
		}
	}
}
