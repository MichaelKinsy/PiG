package activity

import (
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/jsvalue"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/transcript"
)

func assistant(key string, ts int64, stop ai.StopReason, streaming bool, tools []transcript.ToolView, blocks ...ai.AssistantContentBlock) transcript.AssistantItem {
	return transcript.AssistantItem{Key: key, Message: &ai.AssistantMessage{Content: blocks, StopReason: stop, Timestamp: ts}, Tools: tools, Streaming: streaming}
}

func TestThoughtThenAnswerIsOneRowAndAnAnswer(t *testing.T) {
	items := []transcript.Item{
		transcript.UserItem{Key: "u1", Text: "hi"},
		assistant("a1", 1000, ai.StopReasonStop, false, nil, ai.ThinkingContent{Thinking: "hm"}, ai.TextContent{Text: "Echo: hi"}),
	}
	got := Items(items, false)
	if len(got) != 3 {
		t.Fatalf("items = %#v", got)
	}
	act, ok := got[1].(Activity)
	if !ok || len(act.Entries) != 1 || act.Entries[0].Kind != EntryThought {
		t.Fatalf("activity = %#v", got[1])
	}
	words, extra, _, _ := act.Face()
	if words != "Thought" || extra != " · 1 step" {
		t.Fatalf("face = %q %q", words, extra)
	}
	if ans, ok := got[2].(Answer); !ok || ans.Text != "Echo: hi" {
		t.Fatalf("answer = %#v", got[2])
	}
}

func TestCallsSummaryAndLabels(t *testing.T) {
	bash := func(id, cmd string, status transcript.ToolStatus, out string) transcript.ToolView {
		return transcript.ToolView{ID: id, Name: "bash", Args: jsvalue.Obj("command", cmd), Status: status, Output: out}
	}
	read := transcript.ToolView{ID: "r", Name: "read", Args: jsvalue.Obj("path", "/a/b/c.go"), Status: transcript.ToolDone}
	edit := transcript.ToolView{ID: "e", Name: "edit", Args: jsvalue.Obj("path", "/a/b/c.go"), Status: transcript.ToolDone}
	tools := []transcript.ToolView{bash("b1", "go test ./...", transcript.ToolDone, "ok\n"), bash("b2", "rg foo", transcript.ToolError, "boom\nmore"), read, edit}
	items := []transcript.Item{
		transcript.UserItem{Key: "u1", Text: "go"},
		assistant("a1", 1000, ai.StopReasonToolUse, false, tools,
			ai.ThinkingContent{Thinking: "plan"},
			ai.ToolCall{ID: "b1", Name: "bash"}, ai.ToolCall{ID: "b2", Name: "bash"}, ai.ToolCall{ID: "r", Name: "read"}, ai.ToolCall{ID: "e", Name: "edit"}),
		assistant("a2", 4000, ai.StopReasonStop, false, nil, ai.TextContent{Text: "done"}),
	}
	got := Items(items, false)
	act := got[1].(Activity)
	if len(act.Steps) != 4 || len(act.Entries) != 5 {
		t.Fatalf("steps %d entries %d", len(act.Steps), len(act.Entries))
	}
	if w := act.Done(); w != "Thought, ran 2 commands, read a file, edited a file" {
		t.Fatalf("done = %q", w)
	}
	if got, want := Summary(act.Steps, true), "Ran 2 commands, read a file, edited a file · 1 failed"; got != want {
		t.Fatalf("summary = %q", got)
	}
	if act.StartedAt != 1000 || act.EndedAt != 4000 || Elapsed(act.EndedAt-act.StartedAt) != "3s" {
		t.Fatalf("time %d..%d", act.StartedAt, act.EndedAt)
	}
	if d := Describe(act.Steps[1]); d.Label.Verb != "Searched" || d.Description != "boom" {
		t.Fatalf("describe = %#v", d)
	}
	if l := Live(Step{Tool: bash("x", "make", transcript.ToolRunning, ""), Status: StepRunning}); l.Verb != "Running" {
		t.Fatalf("live = %#v", l)
	}
}

func TestWorkingBeforeAnyResponse(t *testing.T) {
	got := Items([]transcript.Item{transcript.UserItem{Key: "u1"}}, true)
	act, ok := got[1].(Activity)
	if !ok || !act.Working {
		t.Fatalf("got %#v", got)
	}
	if w, _, _, _ := act.Face(); w != "Working…" {
		t.Fatalf("face %q", w)
	}
}
