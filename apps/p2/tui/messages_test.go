// pi: apps/p2/test/p2/tui-render.test.ts (the transcript test, for what needs no plugin renderers)
package tui

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

func renderText(t *testing.T, view harness.TranscriptView, expanded bool) string {
	t.Helper()
	built := transcript.BuildTranscript(view, nil, nil)
	component, err := TranscriptComponent(&built, false, expanded, TranscriptOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lines := component.Render(80)
	for i, line := range lines {
		if widthx.VisibleWidth(line) > 80 {
			t.Fatalf("line %d wider than 80", i)
		}
		lines[i] = strings.TrimRight(widthx.StripTerminalSequences(line), " ")
	}
	return strings.Join(lines, "\n")
}

func entry(id int, kind string, model ...ai.Message) harness.TranscriptEntry {
	return harness.TranscriptEntry{Entry: durable.EntryRecord{Id: durable.EntryId(id), ConversationId: durable.ROOT_CONVERSATION_ID, Kind: kind, Model: model}}
}

func TestTranscriptComponentShowsMessages(t *testing.T) {
	view := harness.TranscriptView{Items: []harness.TranscriptItem{
		entry(1, "pi.user", ai.UserMessage{Content: ai.UserText("hi there"), Timestamp: 1}),
		entry(2, "pi.assistant", ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: "Hello **world**"}}, StopReason: ai.StopReasonStop, Timestamp: 2}),
	}}
	out := renderText(t, view, false)
	for _, want := range []string{"hi there", "Hello world"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestTranscriptComponentInterruptedCalls(t *testing.T) {
	call := ai.ToolCall{ID: "t1", Name: "bash", Arguments: ai.JsonObject{"command": "ls"}}
	view := harness.TranscriptView{Items: []harness.TranscriptItem{
		entry(1, "pi.assistant", ai.AssistantMessage{Content: []ai.AssistantContentBlock{call}, StopReason: ai.StopReasonAborted, Timestamp: 1}),
	}}
	if out := renderText(t, view, false); !strings.Contains(out, "Not run: the answer was interrupted.") {
		t.Fatalf("no interrupted card:\n%s", out)
	}
}

type notes struct {
	text []string
	kind []runtime.NoticeKind
}

func (n *notes) Notify(message string, kind runtime.NoticeKind) {
	n.text, n.kind = append(n.text, message), append(n.kind, kind)
}

func TestGrantNotice(t *testing.T) {
	for _, tc := range []struct {
		result runtime.GrantResult
		text   string
		kind   runtime.NoticeKind
	}{
		{runtime.GrantResult{OK: true}, "Done", runtime.NoticeInfo},
		{runtime.GrantResult{Error: "failed"}, "The launcher could not do that (see its message)", runtime.NoticeWarning},
		{runtime.GrantResult{Error: "busy"}, "Not done (busy)", runtime.NoticeWarning},
		{runtime.GrantResult{Error: "denied"}, "", ""},
	} {
		var n notes
		GrantNotice(&n, "Done", tc.result)
		if tc.text == "" {
			if len(n.text) != 0 {
				t.Fatalf("%+v notified %q", tc.result, n.text)
			}
			continue
		}
		if len(n.text) != 1 || n.text[0] != tc.text || n.kind[0] != tc.kind {
			t.Fatalf("%+v: got %q %q", tc.result, n.text, n.kind)
		}
	}
}
