package codingagent_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/tui"
)

// Issue #402. Terminal.app (default profile, Option-as-Meta off) sends Option+Up as plain Up, ESC [ A. Pi reads that as history recall, so a
// queued message appears in the editor as a copy and is still sent after the tool. Divergence D228 makes plain Up dequeue in Terminal.app
// with an empty editor and a queued message, and says once, elsewhere, that the recalled text is a copy.

// holdTool blocks in Execute until release closes, so a message typed meanwhile is queued as a steering message.
type holdTool struct {
	started chan struct{}
	release chan struct{}
}

func (holdTool) Name() string                           { return "hold" }
func (holdTool) Label() string                          { return "hold" }
func (holdTool) Schema() ai.ToolSchema                  { return ai.ToolSchema{Name: "hold"} }
func (holdTool) ExecutionMode() agent.ToolExecutionMode { return agent.ToolModeSequential }
func (h holdTool) Execute(ctx context.Context, _ string, _ json.RawMessage, _ agent.ToolUpdateCallback) (agent.AgentToolResult, error) {
	close(h.started)
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return agent.AgentToolResult{Content: []ai.ToolResultMessageContent{ai.TextContent{Text: "held"}}}, nil
}

func toolCallReply(name string) scriptedReply {
	return func() *ai.AssistantMessage {
		return &ai.AssistantMessage{
			Content:  []ai.AssistantContentBlock{ai.ToolCall{ID: "call-1", Name: name, Arguments: ai.JsonObject{}}},
			Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonToolUse, Usage: ai.Usage{Input: 10, Output: 10},
			Timestamp: time.Now().UnixMilli(),
		}
	}
}

// queuedDuringTool starts a turn that calls a blocking tool and queues "also update the docs" as a steering message while the tool runs.
// The provider answers a third request, which only happens when that message is sent after the tool.
func queuedDuringTool(t *testing.T) (pair *sessionPair, release chan struct{}) {
	t.Helper()
	tool := holdTool{started: make(chan struct{}), release: make(chan struct{})}
	pair = newSessionPairWithTools(t, `{"retry":{"enabled":false}}`, 100000, nil, []agent.AgentTool{tool},
		toolCallReply("hold"),
		reply("tool done", ai.Usage{Input: 10, Output: 10}),
		reply("queued answer", ai.Usage{Input: 10, Output: 10}),
	)
	pair.harness.Do(func() { pair.harness.Enter("start") })
	<-tool.started
	pair.harness.Do(func() {
		pair.harness.SetEditorText("also update the docs")
		pair.harness.Key("\r")
	})
	if steering, followUp := pair.harness.QueuedMessages(); steering+followUp != 1 {
		t.Fatalf("queued during the tool: steering=%d followUp=%d, want exactly one", steering, followUp)
	}
	return pair, tool.release
}

func finishTool(t *testing.T, pair *sessionPair, release chan struct{}) {
	t.Helper()
	close(release)
	pair.harness.WaitIdle(t, 10*time.Second)
	time.Sleep(200 * time.Millisecond)
}

const plainUp = "\x1b[A"

// Terminal.app: plain Up with an empty editor and a queued message runs app.message.dequeue. The owner's flow: Up, clear the editor, let the
// tool finish; nothing queued is sent.
func TestInteractiveTerminalAppPlainUpDequeuesQueuedMessage(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	icodingagent.BindDequeueToAltUp(t) // D228 acts only while the action is bound to alt+up, which Windows keybindings do not do by default
	pair, release := queuedDuringTool(t)
	pair.harness.Do(func() { pair.harness.Key(plainUp) })
	if got := pair.harness.EditorText(); got != "also update the docs" {
		t.Errorf("editor after Up = %q, want the queued message restored", got)
	}
	if steering, followUp := pair.harness.QueuedMessages(); steering != 0 || followUp != 0 {
		t.Errorf("queues after Up: steering=%d followUp=%d, want empty", steering, followUp)
	}
	if got := pair.harness.PendingDisplay(); strings.TrimSpace(got) != "" {
		t.Errorf("pending display after Up = %q, want empty", got)
	}
	pair.harness.Do(func() { pair.harness.SetEditorText("") })
	finishTool(t, pair, release)
	// A queued steering message rides in the request that carries the tool result, so a request count cannot tell it from the dequeued case:
	// no request body may contain the text.
	requests := pair.provider.requestLog()
	for i, body := range requests {
		if strings.Contains(body, "also update the docs") {
			t.Errorf("model request %d of %d carries the dequeued message: %s", i+1, len(requests), body)
		}
	}
}

// Terminal.app keeps Pi's Up in every other state: a non-empty editor moves the cursor, and no queued message leaves history recall.
func TestInteractiveTerminalAppPlainUpKeepsPiBehaviorOutsideTheDequeueState(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "Apple_Terminal")
	icodingagent.BindDequeueToAltUp(t)
	t.Run("nothing queued recalls history", func(t *testing.T) {
		pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil, reply("answer", ai.Usage{Input: 10, Output: 10}))
		pair.harness.Do(func() { pair.harness.Enter("first prompt") })
		pair.harness.WaitIdle(t, 10*time.Second)
		pair.harness.Do(func() { pair.harness.Key(plainUp) })
		if got := pair.harness.EditorText(); got != "first prompt" {
			t.Fatalf("editor after Up = %q, want the history entry", got)
		}
		if got := pair.harness.BelowEditor(); strings.TrimSpace(got) != "" {
			t.Fatalf("rows under the editor = %q, want no tip without a queued message", got)
		}
	})
	t.Run("non-empty editor", func(t *testing.T) {
		pair, release := queuedDuringTool(t)
		pair.harness.Do(func() {
			pair.harness.SetEditorText("draft")
			pair.harness.Key(plainUp)
		})
		if got := pair.harness.EditorText(); got != "draft" {
			t.Fatalf("editor after Up = %q, want the draft kept", got)
		}
		if steering, followUp := pair.harness.QueuedMessages(); steering+followUp != 1 {
			t.Fatalf("queues after Up in a non-empty editor: steering=%d followUp=%d, want the message still queued", steering, followUp)
		}
		pair.harness.Do(func() { pair.harness.SetEditorText("") })
		finishTool(t, pair, release)
	})
}

// Elsewhere (tmux, ssh, unknown or unset TERM_PROGRAM): plain Up recalls history as Pi does, the message stays queued, and one tip line under
// the editor says so, once per process run.
func TestInteractiveQueuedHistoryRecallShowsTheCopyTipOncePerRun(t *testing.T) {
	for _, program := range []string{"", "tmux", "ghostty", "SomethingNew"} {
		t.Run("TERM_PROGRAM="+program, func(t *testing.T) {
			t.Setenv("TERM_PROGRAM", program)
			icodingagent.BindDequeueToAltUp(t)
			t.Setenv("TERM", "tmux-256color")
			pair, release := queuedDuringTool(t)
			dequeueKey := tui.ActionKeyDisplayText("app.message.dequeue")

			pair.harness.Do(func() { pair.harness.Key(plainUp) })
			if got := pair.harness.EditorText(); got != "also update the docs" {
				t.Fatalf("editor after Up = %q, want history recall of the queued text", got)
			}
			if steering, followUp := pair.harness.QueuedMessages(); steering+followUp != 1 {
				t.Fatalf("queues after Up: steering=%d followUp=%d, want the message still queued (Pi)", steering, followUp)
			}
			tip := "↳ That's a copy. The queued message still sends. " + dequeueKey + " edits the queue."
			below := pair.harness.BelowEditor()
			if n := strings.Count(below, "That's a copy."); n != 1 {
				t.Fatalf("tip count under the editor = %d, want 1:\n%s", n, below)
			}
			if !strings.Contains(strings.Join(strings.Fields(below), " "), tip) {
				t.Fatalf("rows under the editor lack %q:\n%s", tip, below)
			}
			// The Terminal.app mapping hint is for terminals that may be Terminal.app behind tmux or ssh, never for one known to send Option+Up.
			wantMapping := program != "ghostty"
			if got := strings.Contains(strings.Join(strings.Fields(below), " "), `map Option+Up to \033[1;3A in Terminal Settings > Profiles > Keyboard.`); got != wantMapping {
				t.Fatalf("mapping hint shown = %v, want %v:\n%s", got, wantMapping, below)
			}
			if strings.Contains(below, "Meta") || strings.Contains(strings.ToLower(below), "option as meta") {
				t.Fatalf("tip suggests Option-as-Meta:\n%s", below)
			}

			// Alt+Up empties the queue and the tip goes with it. A second recall in the same run shows no tip.
			pair.harness.Do(func() { pair.harness.Key("\x1b[1;3A") })
			if got := pair.harness.BelowEditor(); strings.TrimSpace(got) != "" {
				t.Fatalf("rows under the editor after dequeue = %q, want none", got)
			}
			pair.harness.Do(func() {
				pair.harness.SetEditorText("second queued")
				pair.harness.Key("\r")
				pair.harness.Key(plainUp)
			})
			if got := pair.harness.EditorText(); got != "second queued" {
				t.Fatalf("editor after the second Up = %q, want history recall", got)
			}
			if got := pair.harness.BelowEditor(); strings.TrimSpace(got) != "" {
				t.Fatalf("second recall in the same run showed a tip: %q", got)
			}
			pair.harness.Do(func() { pair.harness.SetEditorText("") })
			finishTool(t, pair, release)
		})
	}
}

// With no message queued, Up recalls history and shows no tip.
func TestInteractiveHistoryRecallWithoutQueuedMessageShowsNoTip(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "tmux")
	pair := newSessionPair(t, `{"retry":{"enabled":false}}`, 100000, nil, reply("answer", ai.Usage{Input: 10, Output: 10}))
	pair.harness.Do(func() { pair.harness.Enter("first prompt") })
	pair.harness.WaitIdle(t, 10*time.Second)
	pair.harness.Do(func() { pair.harness.Key(plainUp) })
	if got := pair.harness.EditorText(); got != "first prompt" {
		t.Fatalf("editor after Up = %q, want the history entry", got)
	}
	if got := pair.harness.BelowEditor(); strings.TrimSpace(got) != "" {
		t.Fatalf("rows under the editor = %q, want none", got)
	}
}
