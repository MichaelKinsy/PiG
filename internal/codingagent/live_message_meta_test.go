package codingagent

// pi: packages/coding-agent/src/modes/interactive/interactive-mode.ts entry_appended, renderSessionEntries

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type liveMetaSeen struct {
	messageType extension.MarkdownMessageType
	id, stamp   string
}

// liveMetaMode is a resumed mode whose extension transformer runs on every surface and records the identity it was handed.
func liveMetaMode(t *testing.T, dir, path string) (*InteractiveMode, func() []liveMetaSeen) {
	t.Helper()
	var mu sync.Mutex
	var got []liveMetaSeen
	m := resumeBashModeFromFile(t, dir, path)
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		MarkdownMessageTypes: []extension.MarkdownMessageType{extension.MarkdownMessageCustom, extension.MarkdownMessageSummary},
		MarkdownTransformer: func(text string, context extension.MarkdownTransformContext) string {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, liveMetaSeen{context.MessageType, context.MessageID, context.Timestamp})
			return text
		},
	}}, "")
	m.toolsExpanded = true
	m.statusLine = NewFooterComponent(&ai.Model{ID: "active"}, "", nil)
	return m, func() []liveMetaSeen {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(got)
	}
}

func liveMetaSession(t *testing.T) (dir string, sess *Session) {
	t.Helper()
	dir = t.TempDir()
	sess, err := NewSessionManagerWithDir(dir, dir).Create("live-meta", "")
	if err != nil {
		t.Fatal(err)
	}
	// A session file is written once it holds an assistant message.
	if _, err := sess.AppendMessage(userMsg("question")); err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "answer"})); err != nil {
		t.Fatal(err)
	}
	return dir, sess
}

func requireNoIdentity(t *testing.T, seen []liveMetaSeen, messageType extension.MarkdownMessageType) {
	t.Helper()
	count := 0
	for _, s := range seen {
		if s.messageType != messageType {
			continue
		}
		count++
		if s.id != "" || s.stamp != "" {
			t.Errorf("live %s message transformed with identity %+v; Pi hands none", messageType, s)
		}
	}
	if count == 0 {
		t.Fatalf("no %s message was transformed: %+v", messageType, seen)
	}
}

// Pi's live entry_appended custom_message calls addMessageToChat(createCustomMessage(...)) with no options (interactive-mode.ts:3563-3575).
func TestEntryAppendedCustomMessageHandsTheTransformersNoIdentity(t *testing.T) {
	dir, sess := liveMetaSession(t)
	entry, err := sess.customMessageEntry("note", "live body", true, nil, true)
	if err != nil || entry.ID == "" || entry.Timestamp == "" {
		t.Fatalf("entry %+v, %v", entry, err)
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	m, seen := liveMetaMode(t, dir, sess.Path())
	m.handleEntryAppended(raw)
	m.chatContainer.Render(60)
	requireNoIdentity(t, seen(), extension.MarkdownMessageCustom)
}

// Pi's _appendCustomMessage renders through the same no-options addMessageToChat.
func TestAppendCustomMessageNowHandsTheTransformersNoIdentity(t *testing.T) {
	dir, sess := liveMetaSession(t)
	m, seen := liveMetaMode(t, dir, sess.Path())
	m.runCtx = t.Context()
	m.uiTaskCh = make(chan func(), 4)
	if err := m.appendCustomMessageNow(extension.CustomMessageRef{CustomType: "note", Content: "live body", Display: true}); err != nil {
		t.Fatal(err)
	}
	(<-m.uiTaskCh)()
	m.chatContainer.Render(60)
	requireNoIdentity(t, seen(), extension.MarkdownMessageCustom)
}

// Pi's boundary compaction redraw adds the new summary with addMessageToChat(createCompactionSummaryMessage(...)) and no options, while the retained
// entries around it are restored through renderSessionEntries with their identity (interactive-mode.ts:3583-3598).
func TestBoundaryCompactionSummaryHandsTheTransformersNoIdentity(t *testing.T) {
	dir, sess := liveMetaSession(t)
	keptID, err := sess.AppendMessage(userMsg("kept question"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "kept answer"})); err != nil {
		t.Fatal(err)
	}
	compactionID, err := sess.AppendCompaction("boundary summary", keptID, 1234, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw json.RawMessage
	for _, entry := range sess.GetBranch() {
		if entry.Base().ID == compactionID {
			raw = entry.Raw()
		}
	}
	if raw == nil {
		t.Fatal("compaction entry not on the branch")
	}
	m, seen := liveMetaMode(t, dir, sess.Path())
	m.handleEntryAppended(raw)
	m.chatContainer.Render(60)
	requireNoIdentity(t, seen(), extension.MarkdownMessageSummary)
}

// A restored transcript still hands each persisted entry's identity to the surfaces (renderSessionEntries fills messageMeta).
func TestRestoredCustomMessageAndSummaryKeepTheirEntryIdentity(t *testing.T) {
	dir, sess := liveMetaSession(t)
	custom, err := sess.customMessageEntry("note", "restored body", true, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	m, seen := liveMetaMode(t, dir, sess.Path())
	m.rebuildChatFromSession()
	m.chatContainer.Render(60)
	want := liveMetaSeen{extension.MarkdownMessageCustom, custom.ID, custom.Timestamp}
	if !slices.Contains(seen(), want) {
		t.Fatalf("restored custom message never transformed with %+v; saw %+v", want, seen())
	}
}
