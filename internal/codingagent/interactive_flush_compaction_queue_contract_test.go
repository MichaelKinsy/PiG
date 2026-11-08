package codingagent

// Ports packages/coding-agent/src/modes/interactive/interactive-mode.ts flushCompactionQueue (4879-4956): the rejection sites that restoreQueue handles, the
// extension-command branches, the input handlers and prompt expansion a queued message goes through, and the pending bash components.

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// flushQueueHandle is a Session handle whose input handlers and prompt validation the test controls, and which records the input event each queued
// message raised.
type flushQueueHandle struct {
	*recordingCompactHandle
	inputErr    error
	failOn      string
	validateErr error
	mu          sync.Mutex
	inputs      []string
	behaviors   []string
}

func (h *flushQueueHandle) RunInputHandlers(_ context.Context, text string, images []ai.ImageContent, _ extension.InputSource, behavior string) (string, []ai.ImageContent, bool, error) {
	h.mu.Lock()
	h.inputs = append(h.inputs, text)
	h.behaviors = append(h.behaviors, behavior)
	h.mu.Unlock()
	if h.inputErr != nil && (h.failOn == "" || h.failOn == text) {
		return text, images, false, h.inputErr
	}
	return text, images, false, nil
}

func (h *flushQueueHandle) ValidatePromptModelAuth(context.Context) error { return h.validateErr }

func newFlushQueueMode(t *testing.T) (*InteractiveMode, *flushQueueHandle) {
	t.Helper()
	m, _ := newTickRenderProbe(t, "regular")
	handle := &flushQueueHandle{recordingCompactHandle: &recordingCompactHandle{agent: m.agent}}
	m.opts.SessionHandle = handle
	m.slashRegistry = NewSlashRegistry()
	m.extCtx = &ExtensionContext{}
	m.abortCtx, m.abortFn = context.WithCancel(t.Context())
	return m, handle
}

func flushedChat(m *InteractiveMode) string {
	return strings.Join(strings.Fields(widthx.StripAnsi(strings.Join(m.chatContainer.Render(300), "\n"))), " ")
}

func queuedTexts(queue []compactionQueuedMessage) []string {
	var texts []string
	for _, msg := range queue {
		texts = append(texts, msg.text)
	}
	return texts
}

// Every place a queued message can be rejected restores the whole queue, clears the Session's queues and shows the failure with the queue's plural, never the
// rejection's own error line: an input handler stage that fails (for the first prompt and for a message queued behind it), a before_agent_start stage that
// rejects the run's prompt, and the Session's model check.
func TestFlushCompactionQueueRestoresAtEveryRejectionSite(t *testing.T) {
	queue := []compactionQueuedMessage{{text: "first", mode: compactionQueueSteer}, {text: "second", mode: compactionQueueFollowUp}}
	for _, tc := range []struct {
		name      string
		willRetry bool
		turnBusy  bool
		setup     func(*InteractiveMode, *flushQueueHandle)
		want      string
	}{
		{"input handlers fail for the first prompt", false, false, func(_ *InteractiveMode, h *flushQueueHandle) { h.inputErr = errors.New("input hook failed") }, "input hook failed"},
		{"input handlers fail for a message queued behind the first", false, true, func(_ *InteractiveMode, h *flushQueueHandle) {
			h.inputErr = errors.New("input hook failed")
		}, "input hook failed"},
		{"input handlers fail for the retry queue", true, true, func(_ *InteractiveMode, h *flushQueueHandle) { h.inputErr = errors.New("input hook failed") }, "input hook failed"},
		{"before_agent_start stage rejects the prompt", false, false, func(m *InteractiveMode, _ *flushQueueHandle) {
			var system agent.AgentMessage
			if err := system.UnmarshalJSON([]byte(`{"role":"system","content":5,"timestamp":1}`)); err != nil {
				t.Fatal(err)
			}
			m.agent.SetMessages([]agent.AgentMessage{system})
		}, "content.filter is not a function"},
		{"Session rejects a prompt without a model", false, false, func(_ *InteractiveMode, h *flushQueueHandle) { h.validateErr = agent.ErrNoModelSelected }, FormatNoModelSelectedMessage()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, handle := newFlushQueueMode(t)
				tc.setup(m, handle)
				m.turnActive.Store(tc.turnBusy)
				m.compactionQueue = slices.Clone(queue)
				m.flushCompactionQueue(t.Context(), tc.willRetry)
				settleSubmissions(m)
				if !slices.Equal(queuedTexts(m.compactionQueue), []string{"first", "second"}) {
					t.Fatalf("queue = %q, want both messages restored; chat %q", queuedTexts(m.compactionQueue), flushedChat(m))
				}
				if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 {
					t.Fatalf("the Session queue keeps %d messages", len(steering)+len(followUps))
				}
				chat := flushedChat(m)
				want := "Error: Failed to send queued messages: " + strings.Join(strings.Fields(tc.want), " ")
				if strings.Count(chat, "Failed to send queued messages") != 1 || !strings.Contains(chat, want) {
					t.Fatalf("chat = %q, want one %q", chat, want)
				}
				if strings.Contains(chat, "Error: "+strings.Join(strings.Fields(tc.want), " ")) {
					t.Fatalf("the rejection's own error line is shown as well: %q", chat)
				}
				display := widthx.StripAnsi(strings.Join(m.pendingMessagesContainer.Render(100), "\n"))
				if !strings.Contains(display, "Steering: first") || !strings.Contains(display, "Follow-up: second") {
					t.Fatalf("pending display after restore = %q", display)
				}
			})
		})
	}
}

// A message that fails to send stops the flush: Pi's loop throws at the failed message, so the messages behind it are never sent, and the whole queue
// comes back.
func TestFlushCompactionQueueStopsAtTheFirstFailedMessage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, handle := newFlushQueueMode(t)
		handle.inputErr, handle.failOn = errors.New("input hook failed"), "b"
		m.turnActive.Store(true)
		m.compactionQueue = []compactionQueuedMessage{{text: "a"}, {text: "b"}, {text: "c"}}
		m.flushCompactionQueue(t.Context(), true)
		settleSubmissions(m)
		if !slices.Equal(handle.inputs, []string{"a", "b"}) {
			t.Fatalf("input events = %q, want a and b only", handle.inputs)
		}
		if !slices.Equal(queuedTexts(m.compactionQueue), []string{"a", "b", "c"}) {
			t.Fatalf("queue = %q", queuedTexts(m.compactionQueue))
		}
		if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 {
			t.Fatalf("the Session queue keeps %d messages", len(steering)+len(followUps))
		}
	})
}

// The retry turn takes every message through the input handlers and prompt template expansion, and queues it with its mode whether or not a run is active:
// a steering message is a steer and a follow-up a follow-up, and the handlers see the streaming behavior only while a run is active.
func TestFlushCompactionQueueRetryQueuesThroughInputHandlersAndExpansion(t *testing.T) {
	for _, busy := range []bool{true, false} {
		t.Run(map[bool]string{true: "run active", false: "no run"}[busy], func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, handle := newFlushQueueMode(t)
				m.promptTemplates = []PromptTemplate{{Name: "review", Content: "Review carefully: $ARGUMENTS"}}
				m.turnActive.Store(busy)
				m.compactionQueue = []compactionQueuedMessage{{text: "/review src", mode: compactionQueueSteer}, {text: "after", mode: compactionQueueFollowUp}}
				m.flushCompactionQueue(t.Context(), true)
				settleSubmissions(m)
				steering, followUp := pendingTexts(m)
				if !slices.Equal(steering, []string{"Review carefully: src"}) || !slices.Equal(followUp, []string{"after"}) {
					t.Fatalf("steering=%q followUp=%q", steering, followUp)
				}
				wantBehaviors := []string{"", ""}
				if busy {
					wantBehaviors = []string{"steer", "followUp"}
				}
				if !slices.Equal(handle.inputs, []string{"/review src", "after"}) || !slices.Equal(handle.behaviors, wantBehaviors) {
					t.Fatalf("input events %q with behaviors %q, want the raw texts with %q", handle.inputs, handle.behaviors, wantBehaviors)
				}
				if m.turnActive.Load() != busy {
					t.Fatal("the retry flush changed the run state")
				}
			})
		})
	}
}

type flushCommandLog struct {
	mu    sync.Mutex
	calls []string
}

func (l *flushCommandLog) handler(name string) extension.CommandHandler {
	return func(_ context.Context, args string) error {
		l.mu.Lock()
		l.calls = append(l.calls, name+":"+args)
		l.mu.Unlock()
		return nil
	}
}

func (l *flushCommandLog) sorted() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Sorted(slices.Values(l.calls))
}

func flushCommandMode(t *testing.T) (*InteractiveMode, *flushQueueHandle, *flushCommandLog) {
	t.Helper()
	m, handle := newFlushQueueMode(t)
	log := &flushCommandLog{}
	m.newRunner = inproc.NewRunner([]extension.Extension{{
		Name:         "commands",
		CommandOrder: []string{"cmda", "cmdb"},
		Commands: map[string]extension.RegisteredCommand{
			"cmda": {Name: "cmda", Handler: log.handler("cmda")},
			"cmdb": {Name: "cmdb", Handler: log.handler("cmdb")},
		},
	}}, t.TempDir())
	return m, handle, log
}

// An extension command in the queue runs immediately with its arguments and never reaches the input handlers or a queue; with every queued message an
// extension command, no turn starts.
func TestFlushCompactionQueueRunsExtensionCommands(t *testing.T) {
	for _, tc := range []struct {
		name      string
		willRetry bool
		busy      bool
	}{{"all commands, idle", false, false}, {"all commands, retry", true, true}, {"all commands, run settling", false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				m, handle, log := flushCommandMode(t)
				m.turnActive.Store(tc.busy)
				m.compactionQueue = []compactionQueuedMessage{{text: "/cmda one two"}, {text: "/cmdb"}}
				m.updatePendingMessagesDisplay()
				if display := widthx.StripAnsi(strings.Join(m.pendingMessagesContainer.Render(100), "\n")); !strings.Contains(display, "Steering: /cmda one two") {
					t.Fatalf("the queued commands are not listed before the flush: %q", display)
				}
				m.flushCompactionQueue(t.Context(), tc.willRetry)
				settleSubmissions(m)
				if got := log.sorted(); !slices.Equal(got, []string{"cmda:one two", "cmdb:"}) {
					t.Fatalf("commands ran %q", got)
				}
				if len(handle.inputs) != 0 {
					t.Fatalf("an extension command raised input events %q", handle.inputs)
				}
				if steering, followUps := m.agent.PendingMessages(); len(steering)+len(followUps) != 0 || len(m.compactionQueue) != 0 {
					t.Fatalf("queues: steering=%d followUp=%d compaction=%d", len(steering), len(followUps), len(m.compactionQueue))
				}
				if m.turnActive.Load() != tc.busy {
					t.Fatal("running extension commands changed the run state")
				}
				if display := widthx.StripAnsi(strings.Join(m.pendingMessagesContainer.Render(100), "\n")); strings.Contains(display, "Steering") || strings.Contains(display, "Follow-up") {
					t.Fatalf("the pending display still lists the flushed commands: %q", display)
				}
			})
		})
	}
}

// An idle agent runs the extension commands ahead of the first message that is not one, starts its turn from that message, and sends the later
// messages through their queued modes, an extension command among them running at once instead of being queued. The flush awaits each command, so a
// message behind /cmdb would reach a turn this fake provider has already ended; the follow-up precedes it.
func TestFlushCompactionQueueIdleRunsCommandsThenStartsTheFirstPrompt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, handle, log := flushCommandMode(t)
		m.compactionQueue = []compactionQueuedMessage{
			{text: "/cmda one", mode: compactionQueueSteer}, {text: "prompt", mode: compactionQueueFollowUp},
			{text: "tail", mode: compactionQueueFollowUp}, {text: "/cmdb two", mode: compactionQueueSteer},
		}
		m.flushCompactionQueue(t.Context(), false)
		settleSubmissions(m)
		if got := log.sorted(); !slices.Equal(got, []string{"cmda:one", "cmdb:two"}) {
			t.Fatalf("commands ran %q", got)
		}
		if len(handle.inputs) < 1 || handle.inputs[0] != "prompt" || handle.behaviors[0] != "" {
			t.Fatalf("input events %q with behaviors %q: the first prompt must be the first event, raised while idle", handle.inputs, handle.behaviors)
		}
		if len(handle.inputs) != 2 || handle.inputs[1] != "tail" || handle.behaviors[1] != "followUp" {
			t.Fatalf("input events %q with behaviors %q: the last message follows up into the started turn", handle.inputs, handle.behaviors)
		}
		if len(m.compactionQueue) != 0 {
			t.Fatalf("queue = %q", queuedTexts(m.compactionQueue))
		}
	})
}

// A first prompt keeps its queued mode: a follow-up that finds the run still settling follows up, with the follow-up behavior in its input event.
func TestFlushCompactionQueueFirstPromptKeepsItsModeWhileTheRunSettles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, handle, _ := flushCommandMode(t)
		m.turnActive.Store(true)
		m.compactionQueue = []compactionQueuedMessage{{text: "/cmda", mode: compactionQueueSteer}, {text: "prompt", mode: compactionQueueFollowUp}}
		m.flushCompactionQueue(t.Context(), false)
		settleSubmissions(m)
		steering, followUp := pendingTexts(m)
		if len(steering) != 0 || !slices.Equal(followUp, []string{"prompt"}) || !slices.Equal(handle.behaviors, []string{"followUp"}) {
			t.Fatalf("steering=%q followUp=%q behaviors=%q", steering, followUp, handle.behaviors)
		}
	})
}

// Pi's flush calls session.prompt, not the editor's submit handler, so the commands run during a run stay pending until the run settles (Pi acd059b72
// moves them into the chat then, D311); only a prompt the user submits while idle moves them earlier.
func TestFlushCompactionQueueLeavesPendingBashOutputInThePendingArea(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newFlushQueueMode(t)
		block := tui.NewBashExecutionComponent("echo pending", m.tuiInst, false, m.outputPad)
		m.pendingBashBlocks = []*tui.BashExecutionComponent{block}
		m.pendingMessagesContainer.Add(block)
		m.compactionQueue = []compactionQueuedMessage{{text: "after compaction", mode: compactionQueueSteer}}
		m.flushCompactionQueue(t.Context(), false)
		if len(m.pendingBashBlocks) != 1 || strings.Contains(flushedChat(m), "echo pending") {
			t.Fatalf("the flush moved the pending bash block into the chat (pending=%d, chat %q)", len(m.pendingBashBlocks), flushedChat(m))
		}
		settleSubmissions(m)
	})
}

// Messages restored after a rejected flush stay queued until the next compaction ends: Pi never drains them from the next submission.
func TestSubmitDoesNotDrainRestoredCompactionQueue(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, handle := newFlushQueueMode(t)
		m.compactionQueue = []compactionQueuedMessage{{text: "restored", mode: compactionQueueSteer}}
		m.turnActive.Store(true)
		m.handleSubmit(t.Context(), "typed")
		settleSubmissions(m)
		if !slices.Equal(queuedTexts(m.compactionQueue), []string{"restored"}) || slices.Contains(handle.inputs, "restored") {
			t.Fatalf("queue=%q input events=%q: the submission drained the restored queue", queuedTexts(m.compactionQueue), handle.inputs)
		}
	})
}

// A compaction cancelled with Esc leaves a cancelled abort context behind; the turn the flush starts from an idle agent runs under a fresh one, and the
// context it replaces is released.
func TestFlushCompactionQueueStartsTheTurnUnderALiveAbortContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newFlushQueueMode(t)
		m.abortFn()
		if m.abortCtx.Err() == nil {
			t.Fatal("the abort context is not cancelled")
		}
		m.compactionQueue = []compactionQueuedMessage{{text: "after", mode: compactionQueueSteer}}
		m.flushCompactionQueue(t.Context(), false)
		if m.abortCtx.Err() != nil {
			t.Fatal("the flush left the cancelled abort context for the new turn")
		}
	})
	synctest.Test(t, func(t *testing.T) {
		m, _ := newFlushQueueMode(t)
		previous := m.abortCtx
		m.compactionQueue = []compactionQueuedMessage{{text: "after", mode: compactionQueueSteer}}
		m.flushCompactionQueue(t.Context(), false)
		if previous.Err() == nil || m.abortCtx == previous {
			t.Fatal("the flush kept the abort context of the run that ended")
		}
	})
}

// A flush into a run that is still settling steers into that run and leaves Esc bound to it: the run's abort context is neither cancelled nor replaced.
func TestFlushCompactionQueueIntoASettlingRunKeepsItsAbortContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, _ := newFlushQueueMode(t)
		previous := m.abortCtx
		m.turnActive.Store(true)
		m.compactionQueue = []compactionQueuedMessage{{text: "after", mode: compactionQueueSteer}}
		m.flushCompactionQueue(t.Context(), false)
		if m.abortCtx != previous || previous.Err() != nil {
			t.Fatal("the flush replaced or cancelled the abort context of the run still settling")
		}
	})
}
