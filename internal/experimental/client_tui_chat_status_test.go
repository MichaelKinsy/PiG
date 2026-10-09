package experimental

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
)

// upstream: packages/coding-agent/src/experimental/client-tui-chat.ts:109-128. The status line names the first of: a generation retry, a deferred response, the first live compaction (retrying or running), a running tool, and a plain run; nothing live shows no status.
func TestClientChatViewStatusText(t *testing.T) {
	observation := newClientTuiObservation(t)
	view := NewExperimentalChatView(t.Context(), t.TempDir(), observation.RequestRender, observation.Executor.RunOnMain)
	t.Cleanup(func() {
		if err := view.Dispose(); err != nil {
			t.Error(err)
		}
	})
	taskID := durable.TaskId(3)
	run := &harness.LiveRun{TaskId: 1}
	retry := &harness.RetryStatus{At: 1, Error: "rate limited"}
	for _, row := range []struct {
		name string
		live *harness.LiveState
		want string
	}{
		{"a run", &harness.LiveState{Run: run}, "Working... (esc to abort)"},
		{"a retry outranks everything", &harness.LiveState{Run: run, Generation: &harness.LiveGeneration{Attempt: 1, Retry: retry, Deferred: &harness.DeferredStatus{}}, Compactions: []harness.CompactionStatus{{Reason: "threshold"}}}, "Retrying (attempt 2): rate limited"},
		{"a deferred response", &harness.LiveState{Run: run, Generation: &harness.LiveGeneration{Deferred: &harness.DeferredStatus{PollAt: 1}}, Compactions: []harness.CompactionStatus{{Reason: "threshold"}}}, "Waiting for deferred response..."},
		{"a compaction", &harness.LiveState{Run: run, Compactions: []harness.CompactionStatus{{Reason: "threshold"}}}, "Compacting (threshold)..."},
		{"a retrying compaction", &harness.LiveState{Run: run, Compactions: []harness.CompactionStatus{{Reason: "overflow", Attempt: 2, Retry: retry}}}, "Retrying overflow compaction (attempt 3)..."},
		{"a running tool", &harness.LiveState{Run: run, Tools: []harness.ToolSlot{{CallId: "c1", Name: "bash", TaskId: &taskID, Status: harness.ToolSlotRunning}}}, "Running bash... (esc to abort)"},
		{"nothing live", nil, ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			if err := observation.Executor.RunOnMain(t.Context(), func() {
				if err := view.Apply(conversationView(nil, row.live, nil)); err != nil {
					t.Error(err)
				}
				if view.statusText != row.want {
					t.Errorf("status = %q, want %q", view.statusText, row.want)
				}
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// upstream: packages/coding-agent/src/experimental/client-tui-chat.ts:181-186. A compaction entry shows a muted "[compaction]" line and its summary; a reset entry shows "[new context]".
func TestClientChatViewMarksCompactionAndReset(t *testing.T) {
	observation := newClientTuiObservation(t)
	view := NewExperimentalChatView(t.Context(), t.TempDir(), observation.RequestRender, observation.Executor.RunOnMain)
	t.Cleanup(func() {
		if err := view.Dispose(); err != nil {
			t.Error(err)
		}
	})
	compaction := durable.EntryRecord{Id: 1, ConversationId: durable.ROOT_CONVERSATION_ID, Kind: "pi.compaction", Model: []ai.Message{ai.UserMessage{Content: ai.UserText("the summary")}}}
	if err := observation.Executor.RunOnMain(t.Context(), func() {
		if err := view.Apply(conversationView([]durable.EntryRecord{compaction, resetEntry(2)}, nil, nil)); err != nil {
			t.Error(err)
		}
		rendered := strings.Join(view.Transcript.Render(80), "\n")
		for _, want := range []string{"[compaction]", "the summary", "[new context]"} {
			if !strings.Contains(rendered, want) {
				t.Errorf("transcript lacks %q:\n%s", want, rendered)
			}
		}
		if strings.Index(rendered, "[compaction]") > strings.Index(rendered, "the summary") || strings.Index(rendered, "the summary") > strings.Index(rendered, "[new context]") {
			t.Errorf("transcript order is wrong:\n%s", rendered)
		}
	}); err != nil {
		t.Fatal(err)
	}
}
