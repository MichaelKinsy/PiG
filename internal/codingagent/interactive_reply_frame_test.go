package codingagent_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// replyProvider answers with one text block and pushes every event of the response before the reader takes the first, as the parity harness's
// provider does from one microtask (test/parity/testdata/test-faux-provider.ts:695 queueMicrotask(emitPlan)).
type replyProvider struct{}

func (replyProvider) ID() string   { return "faux" }
func (replyProvider) Close() error { return nil }

func (replyProvider) Stream(context.Context, ai.TranscriptContext, ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	partial := func(text string) *ai.AssistantMessage {
		return &ai.AssistantMessage{Content: []ai.AssistantContentBlock{ai.TextContent{Text: text}}, Provider: "faux", Model: "faux-1", StopReason: ai.StopReasonStop, Timestamp: time.Now().UnixMilli()}
	}
	stream := ai.NewAssistantMessageEventStream()
	for _, event := range []ai.AssistantMessageEvent{
		ai.StartEvent{Partial: partial("")},
		ai.TextStartEvent{ContentIndex: 0, Partial: partial("")},
		ai.TextDeltaEvent{ContentIndex: 0, Delta: "REPLY-TEXT", Partial: partial("REPLY-TEXT")},
		ai.TextEndEvent{ContentIndex: 0, Content: "REPLY-TEXT", Partial: partial("REPLY-TEXT")},
		ai.DoneEvent{Reason: ai.StopReasonStop, Message: partial("REPLY-TEXT")},
	} {
		if err := stream.Push(event); err != nil {
			return nil, err
		}
	}
	return stream, nil
}

// replySession is a real Session on replyProvider with exts loaded in process.
func replySession(t *testing.T, exts ...extension.Extension) (*coding.Session, icodingagent.InteractiveModeOptions) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	agentDir := filepath.Join(home, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: cwd, AgentDir: agentDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := configureFauxAuth(services, "faux-key"); err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner(exts, cwd)
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: replyProvider{}, Capabilities: ai.ModelCapabilities{ContextWindow: 100000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, icodingagent.InteractiveModeOptions{
		CWD: cwd, AgentDir: agentDir, Model: model, SessionHandle: session,
		SettingsManager: services.SettingsManager(), Settings: services.SettingsManager().Get(), ExtensionRunner: runner,
	}
}

// replyFrames is the interactive mode's harness around a replySession, whose loop owns the frames as inputLoop does.
func replyFrames(t *testing.T, onEvent func(*icodingagent.TestHarness, agent.AgentEvent), exts ...extension.Extension) (*icodingagent.TestHarness, *writeLog) {
	t.Helper()
	_, opts := replySession(t, exts...)
	out := &writeLog{}
	h := icodingagent.NewTestHarnessWithOutput(t, opts, onEvent, out)
	h.Do(h.OwnFrames)
	return h, out
}

// Pi draws the reply and takes the Working indicator away in one frame. Its frame is a timer callback (packages/tui/src/tui.ts:1033-1090
// requestRender: process.nextTick, then setTimeout for the rest of the 16 ms), and a timer callback cannot run inside a task of the event loop.
// The response's events are one task when the provider delivers them in one run, and the loop emits turn_end and agent_end back to back behind
// message_end (packages/agent/src/agent-loop.ts:262-263, 296-299, 329), so interactive-mode.ts handles message_update (the text), message_end
// (:3685) and agent_end (:3778, which clears the indicator) before any frame begins.
//
// PiG's owner loop takes the frame timer and the Session's events from one select, so the timer could fire between two of those events:
// scenario interactive-rendering/68 then saw a second frame, which repainted the row of the indicator, begin after the frame with the reply.
// Here the frame deadline passes after every event of the response the loop takes, which is that schedule made certain.
func TestInteractiveReplyAndWorkingRemovalAreOneFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		responding, ticks := false, 0
		h, out := replyFrames(t, func(h *icodingagent.TestHarness, ev agent.AgentEvent) {
			switch ev := ev.(type) {
			case agent.MessageStartEvent:
				responding = responding || ev.Message.Assistant != nil
			case agent.AgentEndEvent:
				responding = false
			}
			if !responding {
				return
			}
			// The clock is the bubble's: the 16 ms deadline of a requested frame passes while the loop is still in this iteration.
			time.Sleep(20 * time.Millisecond)
			h.RunDueFrame()
			ticks++
		})
		before := len(out.snapshot())
		h.Do(func() { h.Enter("hello") })
		h.WaitIdle(t, 10*time.Second)
		// A frame that is still pending is at most one 16 ms throttle away.
		time.Sleep(200 * time.Millisecond)
		if ticks < 6 {
			t.Fatalf("the frame deadline passed %d times inside the response, want once per event (message_start, three updates, message_end, turn_end)", ticks)
		}
		writes := out.snapshot()[before:]
		drawn := []int{}
		for i, w := range writes {
			if strings.Contains(w, "REPLY-TEXT") {
				drawn = append(drawn, i)
			}
		}
		if len(drawn) != 1 {
			t.Fatalf("the reply was drawn by %d writes, want one frame; writes: %q", len(drawn), writes)
		}
		// Pi 1.5.2 draws one frame for the reply and the removal of the indicator: no later write changes a row. A frame that changes none
		// writes only where the cursor goes, and whether one follows is the subject of TestInteractiveAnswerEndEmitsNoTrailingFrame.
		for _, w := range writes[drawn[0]+1:] {
			if strings.Contains(w, "\x1b[2K") || strings.Contains(w, "\x1b[?2026h") {
				t.Fatalf("writes after the write that draws the reply = %q: a later frame repainted a row, so the Working indicator was not taken away by the frame of the reply", writes[drawn[0]+1:])
			}
		}
	})
}

// An extension handler that waits ends the task: Pi's loop turns while agent-session.ts awaits the handler (_handleAgentEvent awaits
// _emitExtensionEvent before it notifies the interactive mode), so the frame that the text requested runs meanwhile. The events before the
// handler are therefore applied and drawn while it waits, whichever of the run's end events it handles, and the loop is never held for it.
func TestInteractiveDrawsTheReplyWhileAnExtensionHandlerWaits(t *testing.T) {
	for _, event := range []string{"message_end", "turn_end", "agent_end"} {
		t.Run(event, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				entered, release := make(chan struct{}), make(chan struct{})
				var once sync.Once
				ext := extension.Extension{Name: "wait", Path: "/ext/wait.ts", Handlers: map[string][]extension.HandlerFn{
					event: {func(args ...any) (any, error) {
						if end, ok := args[0].(extension.MessageEndEvent); ok && end.Message.Assistant == nil {
							return nil, nil
						}
						once.Do(func() { close(entered) })
						<-release
						return nil, nil
					}},
				}}
				h, out := replyFrames(t, nil, ext)
				before := len(out.snapshot())
				h.Do(func() { h.Enter("hello") })
				<-entered
				time.Sleep(100 * time.Millisecond)
				if drawn := strings.Join(out.snapshot()[before:], ""); !strings.Contains(drawn, "REPLY-TEXT") {
					t.Fatalf("the reply is not drawn while the %s handler waits; writes: %q", event, out.snapshot()[before:])
				}
				// The loop still runs what is posted to it.
				ran := false
				h.Do(func() { ran = true })
				if !ran {
					t.Fatal("the owner loop did not run a posted task while the handler waited")
				}
				close(release)
				h.WaitIdle(t, 10*time.Second)
			})
		})
	}
}

// sessionEventLog runs ask on a new replySession and returns, in the order they happened, what a Subscribe listener received ("L:") and what
// a consumer of Events received ("E:", with "E:{" and "E:}" for the markers that bound a task).
func sessionEventLog(t *testing.T, ask func(ctx context.Context, session *coding.Session) error) []string {
	t.Helper()
	session, _ := replySession(t)
	var mu sync.Mutex
	var log []string
	add := func(entry string) {
		mu.Lock()
		log = append(log, entry)
		mu.Unlock()
	}
	session.Subscribe(func(ev agent.AgentEvent) { add(fmt.Sprintf("L:%T", ev)) })
	events := session.Events()
	go func() {
		for ev := range events {
			if marker, ok := ev.(interface{ EventRegion() (open, bounds bool) }); ok {
				if open, bounds := marker.EventRegion(); bounds {
					add(map[bool]string{true: "E:{", false: "E:}"}[open])
					continue
				}
			}
			if coding.AcknowledgeEvent(ev) {
				continue
			}
			add(fmt.Sprintf("E:%T", ev))
		}
	}()
	if err := ask(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if err := session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	return slices.Clone(log)
}

func withPrefix(log []string, prefix string) []string {
	var out []string
	for _, entry := range log {
		if strings.HasPrefix(entry, prefix) {
			out = append(out, entry)
		}
	}
	return out
}

// Only a run the interactive mode starts reports the tasks of Pi's loop: a run of any other owner (print mode's Send, an RPC or SDK caller of
// RunAgentPrompt) puts on Events exactly the events it put there before, with no marker among them.
func TestOnlyTheInteractiveRunPutsTaskMarkersOnEvents(t *testing.T) {
	agentSend := func(ctx context.Context, session *coding.Session) ([]agent.AgentMessage, error) {
		return session.Agent().Send(ctx, "hello")
	}
	sent := sessionEventLog(t, func(ctx context.Context, session *coding.Session) error {
		_, err := session.Send(ctx, "hello")
		return err
	})
	plain := sessionEventLog(t, func(ctx context.Context, session *coding.Session) error {
		_, err := session.RunAgentPrompt(ctx, func(ctx context.Context) ([]agent.AgentMessage, error) { return agentSend(ctx, session) })
		return err
	})
	marked := sessionEventLog(t, func(ctx context.Context, session *coding.Session) error {
		_, err := session.RunAgentPrompt(jsloop.WithTasks(ctx), func(ctx context.Context) ([]agent.AgentMessage, error) { return agentSend(ctx, session) })
		return err
	})
	for name, log := range map[string][]string{"Send": sent, "RunAgentPrompt": plain} {
		if slices.Contains(log, "E:{") || slices.Contains(log, "E:}") {
			t.Fatalf("%s without a request for tasks put a task marker on Events: %q", name, log)
		}
		if got := withPrefix(log, "E:agent."); len(got) == 0 {
			t.Fatalf("%s delivered no agent event on Events: %q", name, log)
		}
	}
	consumer := withPrefix(marked, "E:")
	open, closed := slices.Index(consumer, "E:{"), slices.Index(consumer, "E:}")
	if open < 0 || closed < open || strings.Count(strings.Join(consumer, " "), "E:{") != 1 || strings.Count(strings.Join(consumer, " "), "E:}") != 1 {
		t.Fatalf("the interactive run's Events hold no single pair of task markers: %q", consumer)
	}
	if got, want := consumer[open+1:closed], []string{
		"E:agent.MessageStartEvent", "E:agent.MessageUpdateEvent", "E:agent.MessageUpdateEvent", "E:agent.MessageUpdateEvent",
		"E:agent.MessageEndEvent", "E:agent.TurnEndEvent", "E:agent.AgentEndEvent",
	}; !slices.Equal(got, want) {
		t.Fatalf("the events between the task markers = %q, want the response and the loop's end events %q", got, want)
	}
	unmarked := slices.DeleteFunc(slices.Clone(consumer), func(entry string) bool { return entry == "E:{" || entry == "E:}" })
	if want := withPrefix(plain, "E:"); !slices.Equal(unmarked, want) {
		t.Fatalf("without its markers the interactive run's Events = %q, want the events of the same run without tasks %q", unmarked, want)
	}
}

// The markers change nothing a listener sees: it receives every event when it happens, in order, and so before the consumer of Events is told
// that the task which holds them closed.
func TestListenersReceiveATasksEventsBeforeItsClosingMarker(t *testing.T) {
	log := sessionEventLog(t, func(ctx context.Context, session *coding.Session) error {
		_, err := session.RunAgentPrompt(jsloop.WithTasks(ctx), func(ctx context.Context) ([]agent.AgentMessage, error) {
			return session.Agent().Send(ctx, "hello")
		})
		return err
	})
	closed := slices.Index(log, "E:}")
	if closed < 0 {
		t.Fatalf("no closing marker on Events: %q", log)
	}
	heard := withPrefix(log[:closed], "L:")
	if len(heard) == 0 || heard[len(heard)-1] != "L:agent.AgentEndEvent" || !slices.Contains(heard, "L:agent.MessageUpdateEvent") {
		t.Fatalf("before the closing marker the listener received %q, want every event of the task through agent_end", heard)
	}
	listener, consumer := withPrefix(log, "L:"), withPrefix(log, "E:agent.")
	for i := range listener {
		listener[i] = "E:" + strings.TrimPrefix(listener[i], "L:")
	}
	if !slices.Equal(listener, consumer) {
		t.Fatalf("the listener received %q, the consumer of Events %q: want the same events in the same order", listener, consumer)
	}
}
