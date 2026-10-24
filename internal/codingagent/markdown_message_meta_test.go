package codingagent

import (
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// Ports the interactive-mode.test.ts / interactive-mode-compaction.test.ts messageMeta cases (Pi #11270): rendering from persisted
// session entries hands each message's entry id and timestamp to the Markdown transformers, and a live message hands none.
func TestMarkdownTransformContextCarriesPersistedEntryIdentity(t *testing.T) {
	type seen struct {
		messageType extension.MarkdownMessageType
		id, stamp   string
	}
	var mu sync.Mutex
	var got []seen
	install := func(m *InteractiveMode) {
		m.newRunner = inproc.NewRunner([]extension.Extension{{MarkdownTransformer: func(text string, context extension.MarkdownTransformContext) string {
			mu.Lock()
			defer mu.Unlock()
			got = append(got, seen{context.MessageType, context.MessageID, context.Timestamp})
			return text
		}}}, "")
	}
	dir := t.TempDir()
	sess, err := NewSessionManagerWithDir(dir, dir).Create("meta", "")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := sess.AppendMessage(userMsg("restored question"))
	if err != nil {
		t.Fatal(err)
	}
	assistantID, err := sess.AppendMessage(assistantMsg("", ai.TextContent{Text: "restored answer"}))
	if err != nil {
		t.Fatal(err)
	}
	stamps := map[string]string{}
	for _, entry := range sess.GetEntries() {
		stamps[entry.Base().ID] = entry.Base().Timestamp
	}

	m := resumeBashModeFromFile(t, dir, sess.Path())
	install(m)
	m.rebuildChatFromSession()
	m.chatContainer.Render(60)
	for _, want := range []seen{
		{extension.MarkdownMessageUser, userID, stamps[userID]},
		{extension.MarkdownMessageAssistant, assistantID, stamps[assistantID]},
	} {
		if want.id == "" || want.stamp == "" {
			t.Fatalf("entry %+v has no id or timestamp", want)
		}
		if !slices.Contains(got, want) {
			t.Errorf("restored transcript never transformed with %+v; saw %+v", want, got)
		}
	}

	got = nil
	live := resumeBashModeFromFile(t, dir, sess.Path())
	install(live)
	block := live.newUserMessageBlock("live question", messageMeta{})
	block.Render(60)
	assistant := live.newAssistantMessageBlock(messageMeta{})
	assistant.UpdateContent(tui.AssistantMessage{Content: []tui.AssistantContentBlock{{Type: "text", Text: "live answer"}}})
	assistant.Render(60)
	if len(got) == 0 {
		t.Fatal("the live blocks were never transformed")
	}
	for _, s := range got {
		if s.id != "" || s.stamp != "" {
			t.Errorf("live message transformed with identity %+v, want none", s)
		}
	}
}
