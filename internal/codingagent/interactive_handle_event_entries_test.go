package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts handleEvent (3467-3865): the entry_appended case.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

func entryAppended(t *testing.T, entry any) agent.EntryAppendedEvent {
	t.Helper()
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	return agent.EntryAppendedEvent{Entry: raw}
}

func entryRendererMode(t *testing.T) eventProbe {
	t.Helper()
	p := newEventProbe(t, false)
	p.newRunner = inproc.NewRunner([]extension.Extension{{EntryRenderers: map[string]extension.EntryRenderer{
		"note": func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
			return &toggleEntryComponent{content: fmt.Sprintf("%v", entry.Data), expanded: options.Expanded}
		},
	}}}, t.TempDir())
	return p
}

// A custom entry draws through its registered entry renderer; without one it draws nothing. An entry appended while a message streams goes above
// the streaming message.
func TestHandleEventEntryAppendedCustomUsesTheEntryRenderer(t *testing.T) {
	p := entryRendererMode(t)
	p.handleAgentEvent(agent.MessageStartEvent{Message: assistantMsg("", ai.TextContent{Text: "STREAMING"})})
	p.handleAgentEvent(entryAppended(t, CustomEntry{Type: "custom", ID: "e1", CustomType: "note", Data: "BOUNDARY-ENTRY"}))
	p.handleAgentEvent(entryAppended(t, CustomEntry{Type: "custom", ID: "e2", CustomType: "unregistered", Data: "UNSEEN"}))
	got := p.chat()
	entry, streaming := strings.Index(got, "entry:BOUNDARY-ENTRY"), strings.Index(got, "STREAMING")
	if entry < 0 || streaming < 0 || entry > streaming || strings.Contains(got, "UNSEEN") {
		t.Fatalf("chat = %q, want the entry above the streaming message and no unregistered entry", got)
	}
}

// A custom_message entry draws as a message when it is displayed; a hidden one draws nothing.
func TestHandleEventEntryAppendedCustomMessageDrawsWhenDisplayed(t *testing.T) {
	p := newEventProbe(t, false)
	p.handleAgentEvent(entryAppended(t, CustomMessageEntry{Type: "custom_message", ID: "m1", CustomType: "note", Content: "SHOWN BOUNDARY MESSAGE", Display: true}))
	p.handleAgentEvent(entryAppended(t, CustomMessageEntry{Type: "custom_message", ID: "m2", CustomType: "note", Content: "HIDDEN BOUNDARY MESSAGE", Display: false}))
	if got := p.chat(); !strings.Contains(got, "SHOWN BOUNDARY MESSAGE") || strings.Contains(got, "HIDDEN BOUNDARY MESSAGE") {
		t.Fatalf("chat = %q", got)
	}
}

// Entries of other types, and a usage entry that is not a cache warm-up, draw nothing.
func TestHandleEventEntryAppendedIgnoresOtherEntries(t *testing.T) {
	p := newEventProbe(t, false)
	before := p.chat()
	p.handleAgentEvent(entryAppended(t, map[string]any{"type": "label", "id": "l1", "targetId": "x", "label": "LABEL"}))
	p.handleAgentEvent(agent.EntryAppendedEvent{Entry: json.RawMessage(`not json`)})
	if p.chat() != before {
		t.Fatalf("chat = %q", p.chat())
	}
}

func boundaryCompactionMode(t *testing.T, build func(sess *Session, ids map[string]string)) (eventProbe, map[string]string) {
	t.Helper()
	ids := map[string]string{}
	m := resumedModeWithOutputPad(t, 1, func(sess *Session) { build(sess, ids) })
	m.statusLine = NewFooterComponent(nil, "", nil)
	m.opts.Settings = Settings{}
	return eventProbe{InteractiveMode: m, progress: &[]bool{}}, ids
}

func appendMessage(t *testing.T, sess *Session, ids map[string]string, name string, message agent.AgentMessage) {
	t.Helper()
	id, err := sess.AppendMessage(message)
	if err != nil {
		t.Fatal(err)
	}
	ids[name] = id
}

func appendedEvent(t *testing.T, p eventProbe, id string) agent.EntryAppendedEvent {
	t.Helper()
	entry, ok := p.currentSession().GetEntry(id)
	if !ok {
		t.Fatalf("entry %q is not in the session", id)
	}
	return agent.EntryAppendedEvent{Entry: entry.Raw()}
}

// A compaction entry an extension appended at a boundary rebuilds the transcript around its summary: the entries the compaction kept come first, then
// the summary, then the entries after it. Those later entries were drawn by the rebuild, so their own entry_appended events draw nothing.
func TestHandleEventEntryAppendedCompactionRebuildsTheTranscript(t *testing.T) {
	p, ids := boundaryCompactionMode(t, func(sess *Session, ids map[string]string) {
		appendMessage(t, sess, ids, "old", userMsg("OLD QUESTION"))
		appendMessage(t, sess, ids, "oldAnswer", assistantMsg("", ai.TextContent{Text: "OLD ANSWER"}))
		appendMessage(t, sess, ids, "kept", userMsg("KEPT QUESTION"))
		id, err := sess.AppendCompaction("BOUNDARY SUMMARY", ids["kept"], 1234, nil, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["compaction"] = id
		id, err = sess.AppendCustomMessageEntry("note", "AFTER COMPACTION", true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["after"] = id
	})
	p.toolsExpanded = true
	p.chatContainer.Add(tui.NewTruncatedText("STALE TRANSCRIPT", 0, 0))
	p.handleAgentEvent(appendedEvent(t, p, ids["compaction"]))
	got := p.chat()
	kept, summary, after := strings.Index(got, "KEPT QUESTION"), strings.Index(got, "BOUNDARY SUMMARY"), strings.Index(got, "AFTER COMPACTION")
	if strings.Contains(got, "STALE TRANSCRIPT") || strings.Contains(got, "OLD QUESTION") || kept < 0 || summary < 0 || after < 0 || kept >= summary || summary >= after {
		t.Fatalf("chat = %q, want the kept entry, the summary and the later entry in that order and nothing else", got)
	}
	p.handleAgentEvent(appendedEvent(t, p, ids["after"]))
	if again := p.chat(); again != got {
		t.Fatalf("the later entry's own event drew it again: %q", again)
	}
	p.handleAgentEvent(appendedEvent(t, p, ids["after"]))
	if twice := p.chat(); strings.Count(twice, "AFTER COMPACTION") != 2 {
		t.Fatalf("an entry is skipped once only: %q", twice)
	}
}

// A compaction entry that is not the first context entry (a later compaction replaced it) changes nothing.
func TestHandleEventEntryAppendedSupersededCompactionChangesNothing(t *testing.T) {
	p, ids := boundaryCompactionMode(t, func(sess *Session, ids map[string]string) {
		appendMessage(t, sess, ids, "first", userMsg("FIRST QUESTION"))
		id, err := sess.AppendCompaction("FIRST SUMMARY", ids["first"], 100, nil, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		ids["compaction"] = id
		appendMessage(t, sess, ids, "second", userMsg("SECOND QUESTION"))
		if _, err := sess.AppendCompaction("SECOND SUMMARY", ids["second"], 200, nil, true, nil); err != nil {
			t.Fatal(err)
		}
	})
	p.chatContainer.Add(tui.NewTruncatedText("UNTOUCHED", 0, 0))
	p.handleAgentEvent(appendedEvent(t, p, ids["compaction"]))
	if got := p.chat(); !strings.Contains(got, "UNTOUCHED") || strings.Contains(got, "FIRST SUMMARY") {
		t.Fatalf("chat = %q", got)
	}
}
