package extensionconformance

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// The kinds of row that need more than the command's run.
const (
	blockedCancel  = 1
	blockedExit    = 2
	detachedReport = 3
)

// Where a refused send is reported (agent-session.ts at Pi b4afdddf44e0). pi.sendUserMessage and pi.sendMessage call the async session method and hang
// `.catch((err) => runner.emitError(...))` on the Promise it returns (:3544-3546 sendMessage, :3552-3555 sendUserMessage). A refusal such as :2240-2244's
// ("Agent is already processing") rejects that Promise, and the catch callback runs in a microtask: after the synchronous run of the code that made the call
// has ended, so after every effect the run had, whatever the order of the calls. The queue_update of a steer or follow-up sent later in the same run, which
// Pi emits while the call executes, comes first. The code ends its run where it awaits or returns: a handler at its first await or its return, a timer's or
// an I/O callback at its return.
//
// The rows below make three sends in one run of the extension, the first of which names no delivery while the agent streams (refused), then a steer and a
// follow-up. Pi's order is queue_update(steer), queue_update(steer + follow-up), extension error `send_user_message`.
//
//   - first: the three sends in one synchronous run of the command's handler;
//   - between: refused, steer, refused: the steer is queued before either refusal, which keep their count;
//   - yielded: the handler yields first (an await in Pi; ctx.Yield in a thread-based SDK), then sends: the run after the yield is a run like any other;
//   - throws: the handler fails after the sends: the report is not lost;
//   - blocked, then the request is cancelled (the user aborts the run): nothing is reported while the handler's run has not ended; the cancellation ends
//     it and the report comes;
//   - blocked, then the extension exits (its connection ends): the pending report is made at the connection's end;
//   - detached: the same three sends from a timer (Node) or a thread or goroutine the handler started (every other SDK) after the handler returned. A timer
//     callback is a run of its own in Pi, so Node reports after it, as for a handler. A thread-based SDK has no run boundary outside a request: pig
//     divergence, the refusal there is reported when the call arrives, before the queue updates.
//   - detached_alone: a refusal alone, from that code: reported at once, as Pi's microtask would be, whatever the SDK; it waits for no boundary that
//     never comes.
func TestSendRefusalReportedAfterSyncRunAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	const (
		steerQueued = "queue steer=[order-steer] follow=[]"
		bothQueued  = "queue steer=[order-steer] follow=[order-followUp]"
		sendRefused = "error send_user_message: " + agentAlreadyProcessing
	)
	pi := []string{steerQueued, bothQueued, sendRefused}
	rows := []struct {
		arg  string
		want func(eventLoop bool) []string
		kind int
	}{
		{arg: "first", want: func(bool) []string { return pi }},
		{arg: "between", want: func(bool) []string { return []string{steerQueued, sendRefused, sendRefused} }},
		{arg: "yielded", want: func(bool) []string { return pi }},
		{arg: "throws", want: func(bool) []string { return pi }},
		{arg: "blocked", kind: blockedCancel, want: func(bool) []string { return pi }},
		{arg: "blocked", kind: blockedExit, want: func(bool) []string { return pi }},
		{arg: "detached", kind: detachedReport, want: func(eventLoop bool) []string {
			if eventLoop {
				return pi
			}
			return []string{sendRefused, steerQueued, bothQueued}
		}},
		{arg: "detached_alone", kind: detachedReport, want: func(bool) []string { return []string{sendRefused} }},
	}
	for _, tc := range append(allHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			eventLoop := strings.Contains(tc.name, "node")
			for _, row := range rows {
				name := row.arg
				switch row.kind {
				case blockedCancel:
					name += "/cancelled"
				case blockedExit:
					name += "/exits"
				}
				t.Run(name, func(t *testing.T) {
					h := tc.make(t)
					t.Cleanup(func() {
						if h.cleanup != nil {
							h.cleanup()
						}
						if h.host != nil {
							h.host.Shutdown("test done")
						}
					})
					if h.bridge == nil {
						t.Skip("no host bridge for " + tc.name)
					}
					checkSendRefusalOrder(t, h, row.arg, row.kind, row.want(eventLoop))
				})
			}
		})
	}
}

func checkSendRefusalOrder(t *testing.T, h *harness, arg string, kind int, want []string) {
	t.Helper()
	gate, started := make(chan struct{}), make(chan struct{})
	var startedOnce, releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(gate) }) }
	t.Cleanup(release)

	provider := ai.NewFauxProvider(ai.FauxConfig{ProviderID: "faux", Model: "faux-1"})
	hold := ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
		startedOnce.Do(func() { close(started) })
		select {
		case <-gate:
		case <-t.Context().Done():
		}
		return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("first reply")}, ai.FauxAssistantMessageOptions{}), nil
	})
	reply := func() ai.FauxResponseStep {
		return ai.FauxFactoryStep(func(ai.TranscriptContext, ai.StreamOptions, *ai.FauxProviderState, *ai.Model) (ai.AssistantMessage, error) {
			return ai.FauxAssistantMessage(ai.FauxContentBlocks{ai.FauxText("reply")}, ai.FauxAssistantMessageOptions{}), nil
		})
	}
	provider.SetResponses([]ai.FauxResponseStep{hold, reply(), reply(), reply()})

	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	configureFauxAuth(t, services)
	runner := inproc.NewRunner(nil, t.TempDir())
	model := &ai.Model{ID: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 4000, MaxOutputTokens: 1000}}
	session, err := coding.NewSession(services, coding.SessionOptions{Model: model, Runner: runner, SkipBuiltinTools: true, NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })

	// One ordered log of what a consumer of the Session sees: the queue updates its event stream carries and the extension errors its runner emits.
	var mu sync.Mutex
	var log []string
	var recording = true
	record := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		if recording {
			log = append(log, line)
		}
	}
	snapshot := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(log)
	}
	session.Subscribe(func(event agent.AgentEvent) {
		if update, ok := event.(agent.QueueUpdateEvent); ok {
			record("queue steer=[" + strings.Join(update.Steering, ",") + "] follow=[" + strings.Join(update.FollowUp, ",") + "]")
		}
	})
	runner.AddErrorListener(func(err *extension.ExtensionError) { record("error " + err.Event + ": " + err.Error) })
	// The binding every non-interactive mode uses.
	coding.BindSessionExtensionActions(nil, h.bridge, func() *coding.Session { return session }, extension.ContextActions{}, nil)

	prompted := make(chan error, 1)
	go func() { prompted <- session.Prompt(t.Context(), "first") }()
	<-started
	if !session.IsStreaming() {
		t.Fatal("the model call is in flight but the Session is not streaming")
	}

	command, ok := findCommand(h.runner, "send_refusal_order")
	if !ok {
		t.Fatal("send_refusal_order command missing")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan error, 1)
	go func() { handled <- command.Handler(ctx, arg) }()

	waitFor := func(n int) {
		pollUntilConformance(t, "the log never reached "+strings.Join(want[:n], " | "), func() bool { return len(snapshot()) >= n })
	}
	switch kind {
	case blockedCancel, blockedExit:
		// The handler's run has not ended: the steer and the follow-up are queued and the refusal is held with the run.
		waitFor(len(want) - 1)
		time.Sleep(300 * time.Millisecond)
		if got := snapshot(); !slices.Equal(got, want[:len(want)-1]) {
			t.Fatalf("while the handler's run has not ended the Session reported %q, want %q (the refusal waits for the run's end)", got, want[:len(want)-1])
		}
		if kind == blockedCancel {
			cancel()
		} else {
			h.host.Shutdown("the extension exits")
		}
	}
	waitFor(len(want))
	// An extra report would come right behind the last one: a short window after it, in which nothing more may be logged, shows there is none.
	time.Sleep(100 * time.Millisecond)
	if got := snapshot(); !slices.Equal(got, want) {
		t.Fatalf("%s: the Session reported %q, want %q", arg, got, want)
	}
	mu.Lock()
	recording = false
	mu.Unlock()
	if arg == "throws" {
		if err := <-handled; err == nil || !strings.Contains(err.Error(), "order-throws") {
			t.Fatalf("the handler's own failure was %v, want order-throws", err)
		}
	}
	release()
	if err := <-prompted; err != nil {
		t.Fatalf("the streaming prompt: %v", err)
	}
}
