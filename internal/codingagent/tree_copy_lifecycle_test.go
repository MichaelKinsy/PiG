package codingagent

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Upstream tree-selector.ts:getEntryCopyText preserves full content, uses the assistant error only for empty content, and treats whitespace-only text as absent.
func TestTreeNodeCopyTextUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		extra      map[string]any
		want       *string
	}{
		{"text blocks", "message", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": " α\n"}, map[string]any{"type": "thinking", "thinking": "private"}, map[string]any{"type": "text", "text": "β "}}}}, new(" α\nβ ")},
		{"assistant error", "message", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "errorMessage": "failed"}}, new("failed")},
		{"whitespace", "message", map[string]any{"message": map[string]any{"role": "user", "content": " \n\t"}}, nil},
		{"BOM whitespace", "message", map[string]any{"message": map[string]any{"role": "user", "content": "\ufeff"}}, nil},
		{"NEL is text", "message", map[string]any{"message": map[string]any{"role": "user", "content": "\u0085"}}, new("\u0085")},
		{"empty", "message", map[string]any{"message": map[string]any{"role": "user", "content": ""}}, nil},
		{"custom", "custom_message", map[string]any{"customType": "test", "content": " full\ncustom ", "display": true}, new(" full\ncustom ")},
		{"custom blocks", "custom_message", map[string]any{"customType": "test", "content": []any{map[string]any{"type": "text", "text": "one"}, map[string]any{"type": "text", "text": "two"}}, "display": true}, new("onetwo")},
		{"compaction", "compaction", map[string]any{"summary": " full\nsummary "}, new(" full\nsummary ")},
		{"branch", "branch_summary", map[string]any{"summary": "branch"}, new("branch")},
		{"bash", "message", map[string]any{"message": map[string]any{"role": "bashExecution", "command": "echo first\necho second", "output": "not the command"}}, new("echo first\necho second")},
		{"metadata", "model_change", map[string]any{"provider": "test", "modelId": "model"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := upstreamTreeSession(t, "entry", treeCaseEntry{id: "entry", kind: tc.kind, extra: tc.extra})
			adapter := &treeNodeAdapter{n: session.GetTree()[0], f: newTreeRowFormatter(session)}
			if got := adapter.NodeCopyText(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("copy text = %v, want %v", got, tc.want)
			}
		})
	}
}

// interactive-mode.ts:5533-5544 rejects missing copy text and reports clipboard failures on the UI.
func TestTreeCopyReportsAbsenceAndClipboardErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		text    *string
		failure error
		want    string
		calls   int
	}{
		{"absent", nil, nil, "Selected entry has no text to copy", 0},
		{"empty", new(""), nil, "Selected entry has no text to copy", 0},
		{"failure", new("message"), errors.New("clipboard denied"), "clipboard denied", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 120, 40), backgroundCtx: t.Context(), uiTaskCh: make(chan func(), 1)}
			calls := 0
			m.copyClipboard = func(string) error { calls++; return tc.failure }
			m.copySelectedTreeMessage(tc.text)
			m.backgroundTasks.Wait()
			select {
			case apply := <-m.uiTaskCh:
				apply()
			default:
			}
			if calls != tc.calls {
				t.Fatalf("clipboard calls=%d, want %d", calls, tc.calls)
			}
			if got := stripANSITest(strings.Join(m.chatContainer.Render(120), "\n")); !strings.Contains(got, "Error: "+tc.want) {
				t.Fatalf("error output=%q", got)
			}
		})
	}
}

func TestTreeCopyCompletionDoesNotReviveCancelledUI(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	m := &InteractiveMode{chatContainer: tui.NewContainer(), tuiInst: tui.NewWithOutput(io.Discard, 120, 40), backgroundCtx: ctx, uiTaskCh: make(chan func(), 1)}
	m.copyClipboard = func(string) error { close(started); <-release; return errors.New("late clipboard error") }
	m.copySelectedTreeMessage(new("message"))
	<-started
	cancel()
	close(release)
	m.backgroundTasks.Wait()
	select {
	case apply := <-m.uiTaskCh:
		apply()
	default:
	}
	if m.chatContainer.ChildCount() != 0 {
		t.Fatal("clipboard completion repainted a cancelled UI")
	}
}
