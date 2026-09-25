package coding

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A panic while the session dispatches an agent event to extensions used to be
// swallowed by forwardAgentEvents, which then closed the event channel: the UI
// stopped receiving events and the agent blocked once the raw event buffer
// filled, with nothing reported. Upstream reports extension failures through
// runner.emitError, which the modes display, and keeps going. Here the panic
// comes from an error listener that fails while a handler error is reported.
func TestSessionReportsAgentEventPanicAndKeepsForwarding(t *testing.T) {
	ext := extension.Extension{Path: "failing.ts", Handlers: map[string][]extension.HandlerFn{
		"agent_start": {func(...any) (any, error) { return nil, errors.New("handler failed") }},
	}}
	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		if err.ExtensionPath != agentEventErrorPath {
			panic("listener failed")
		}
	})
	var mu sync.Mutex
	var reported []extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		reported = append(reported, *err)
		mu.Unlock()
	})

	sess, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	for i := range 3 {
		sent := make(chan error, 1)
		go func() {
			_, err := sess.Send(context.Background(), "go")
			sent <- err
		}()
		sawStart, sawEnd := false, false
		deadline := time.After(10 * time.Second)
		for !sawEnd {
			select {
			case ev, ok := <-sess.Events():
				if !ok {
					t.Fatalf("turn %d: the session closed its event channel after the panic", i)
				}
				switch ev.(type) {
				case agent.AgentStartEvent:
					sawStart = true
				case agent.AgentEndEvent:
					sawEnd = true
				}
			case <-deadline:
				t.Fatalf("turn %d: agent_end never arrived after the panic", i)
			}
		}
		if !sawStart {
			t.Fatalf("turn %d: the event whose dispatch panicked was not forwarded", i)
		}
		select {
		case <-sent:
		case <-time.After(10 * time.Second):
			t.Fatalf("turn %d: Send blocked after the panic", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 3 {
		t.Fatalf("reported %d errors, want one per turn: %+v", len(reported), reported)
	}
	for _, report := range reported {
		if report.ExtensionPath != agentEventErrorPath || report.Event != "agent_start" || report.Error != "panic: listener failed" || report.Stack == "" {
			t.Fatalf("report = %+v", report)
		}
	}
}

// compaction_start is forwarded synchronously (emitOrderedEventSync waits for
// its dispatch). A compaction_start handler that panics must still release that
// wait, so manual compaction completes and the panic is reported; the later
// listeners on the event and the compaction_end event keep flowing.
func TestManualCompactionCompletesWhenCompactionStartHandlerPanics(t *testing.T) {
	sess := buildSessionWithMessages(t, newTestServicesSmallKeep(t), 3)
	defer func() { _ = sess.Close() }()
	sess.completer = &fakeCompleter{summary: "summary after panic"}
	runner := inproc.NewRunner(nil, t.TempDir())
	var mu sync.Mutex
	var reported []extension.ExtensionError
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		reported = append(reported, *err)
		mu.Unlock()
	})
	sess.ReplaceRunner(runner)
	unsubscribe := sess.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.CompactionStartEvent); ok {
			panic("compaction handler failed")
		}
	})
	defer unsubscribe()

	done := make(chan error, 1)
	go func() {
		result, err := sess.CompactResult(context.Background(), "")
		if err == nil && !strings.Contains(result.Summary, "summary after panic") {
			err = errors.New("unexpected summary " + result.Summary)
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CompactResult: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("manual compaction hung after its compaction_start handler panicked")
	}

	var sawEnd bool
	for _, event := range drainEvents(t, sess) {
		if _, ok := event.(agent.CompactionEndEvent); ok {
			sawEnd = true
		}
	}
	if !sawEnd {
		t.Fatal("compaction_end was not forwarded after the panic")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(reported) != 1 || reported[0].Error != "panic: compaction handler failed" || reported[0].ExtensionPath != agentEventErrorPath {
		t.Fatalf("reported = %+v", reported)
	}
}
