package coding

import (
	"context"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/invocation"
)

// settledLog records the order of the events a settled-handler reproduction observes.
type settledLog struct {
	mu     sync.Mutex
	events []string
}

func (l *settledLog) add(event string) {
	l.mu.Lock()
	l.events = append(l.events, event)
	l.mu.Unlock()
}

func (l *settledLog) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// requestRecorder wraps a scripted reply so the log shows when the provider receives each request.
func (l *settledLog) request(number string, reply scriptedResponse, during func()) scriptedResponse {
	return func(messages []ai.Message) *ai.AssistantMessage {
		l.add("request:" + number)
		if during != nil {
			during()
		}
		return reply(messages)
	}
}

// awaitIdleWaiter starts WaitForIdle in the background and returns once the Session holds the waiter.
func awaitIdleWaiter(t *testing.T, s *Session, log *settledLog) <-chan struct{} {
	t.Helper()
	resolved := make(chan struct{})
	go func() {
		defer close(resolved)
		if err := s.WaitForIdle(context.Background()); err != nil {
			t.Error(err)
		}
		log.add("idle-resolved")
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.runState.mu.Lock()
		registered := s.runState.idle != nil
		s.runState.mu.Unlock()
		if registered {
			return resolved
		}
		if time.Now().After(deadline) {
			t.Fatal("WaitForIdle did not register")
		}
		runtime.Gosched()
	}
}

// Pi #10704 (open, no fix at the pin): a waitForIdle() registered during a run resolved while an awaited agent_settled handler was still finishing
// ctx.compact(), before the continuation the handler queued had run: the compaction's own idle check saw no run and no compaction. A waiter
// registered during the run stays pending through its settlement handlers and their deferred continuations.
func TestWaitForIdleStaysPendingThroughSettledHandlerCompactionAndContinuation(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed atomic.Bool
	var resolved <-chan struct{}
	hooks := summaryOverrideHandlers("summary from extension", nil)
	hooks["agent_settled"] = []extension.HandlerFn{func(args ...any) (any, error) {
		if !armed.CompareAndSwap(true, false) {
			return nil, nil
		}
		done := make(chan struct{}, 2)
		h.session.ExtensionCompact(&extension.CompactOptions{
			OnComplete: func(extension.CompactionResult) { done <- struct{}{} },
			OnError:    func(error) { done <- struct{}{} },
		})
		invocation.Acknowledge(args[1].(context.Context)) // the await of ctx.compact() releases the event executor, as a suspended Promise does
		<-done
		log.add("compact-complete")
		err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "continue", Content: "CONTINUE"}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		log.add("continuation-queued")
		return nil, err
	}}
	h = newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: hooks}, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1}}`},
		fauxReply("warmup", ai.StopReasonStop, 0),
		log.request("1", fauxReply("two", ai.StopReasonStop, 0), func() { resolved = awaitIdleWaiter(t, h.session, log) }),
		log.request("2", fauxReply("three", ai.StopReasonStop, 0), nil))
	if err := h.session.Prompt(t.Context(), "warmup", nil); err != nil {
		t.Fatal(err)
	}
	log.events = nil
	armed.Store(true)
	if err := h.session.Prompt(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resolved:
	case <-time.After(30 * time.Second):
		t.Fatalf("the waiter never resolved: %v", log.snapshot())
	}
	// Pi's expected order: the waiter resolves only after the continuation's request has run.
	want := []string{"request:1", "compact-complete", "continuation-queued", "request:2", "idle-resolved"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Pi #10705 (open, no fix at the pin): session.abort() from a public agent_settled subscriber resolved, yet a continuation an extension had
// already queued from its agent_settled handler still started another provider request. An abort during settlement discards the deferred prompts.
func TestAbortFromAgentSettledSubscriberDiscardsQueuedContinuation(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed, aborted atomic.Bool
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_settled": {func(args ...any) (any, error) {
		if !armed.CompareAndSwap(true, false) {
			return nil, nil
		}
		err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "continue", Content: "CONTINUE"}, &extension.SendMessageOptions{TriggerTurn: new(true)})
		log.add("continuation-queued")
		return nil, err
	}}}}
	h = newRecoveryHarness(t, harnessOptions{extension: ext},
		log.request("1", fauxReply("one", ai.StopReasonStop, 0), nil),
		log.request("2", fauxReply("two", ai.StopReasonStop, 0), nil))
	h.session.Subscribe(func(event agent.AgentEvent) {
		if _, ok := event.(agent.AgentSettledEvent); ok && aborted.CompareAndSwap(false, true) {
			log.add("abort-requested")
			if err := h.session.Abort(context.Background()); err != nil {
				t.Error(err)
			}
			log.add("abort-resolved")
		}
	})
	armed.Store(true)
	if err := h.session.Prompt(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"request:1", "continuation-queued", "abort-requested", "abort-resolved"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (no request after the abort)", got, want)
	}
}

// twoDeferredContinuations returns an agent_settled handler that, once armed, defers a custom-message continuation "a" and then a user prompt "b".
func twoDeferredContinuations(h **recoveryHarness, armed *atomic.Bool, log *settledLog) extension.HandlerFn {
	return func(args ...any) (any, error) {
		if !armed.CompareAndSwap(true, false) {
			return nil, nil
		}
		if err := (*h).session.SendMessage(extension.CustomMessageRef{CustomType: "continue", Content: "a"}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
			return nil, err
		}
		log.add("queued:a")
		if err := (*h).session.SendExtensionUserMessage("b", nil); err != nil {
			return nil, err
		}
		log.add("queued:b")
		return nil, nil
	}
}

// D142 covers every prompt the handlers deferred, not only the first: the first deferred run settles through its own nested settlement, and a
// waiter registered during the run must not resolve while the second deferred prompt is still in its preflight (here a slow input handler).
func TestWaitForIdleStaysPendingThroughEveryDeferredPrompt(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed atomic.Bool
	var resolved <-chan struct{}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"agent_settled": {twoDeferredContinuations(&h, &armed, log)},
		"input": {func(args ...any) (any, error) {
			if event, ok := args[0].(extension.InputEvent); ok && event.Text == "b" {
				// A waiter that resolves early does so while this handler runs; the bound only limits the passing case.
				select {
				case <-resolved:
				case <-time.After(200 * time.Millisecond):
				}
			}
			return nil, nil
		}},
	}}
	h = newRecoveryHarness(t, harnessOptions{extension: ext},
		log.request("1", fauxReply("one", ai.StopReasonStop, 0), func() { resolved = awaitIdleWaiter(t, h.session, log) }),
		log.request("2", fauxReply("two", ai.StopReasonStop, 0), nil),
		log.request("3", fauxReply("three", ai.StopReasonStop, 0), nil))
	armed.Store(true)
	if err := h.session.Prompt(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-resolved:
	case <-time.After(30 * time.Second):
		t.Fatalf("the waiter never resolved: %v", log.snapshot())
	}
	want := []string{"request:1", "queued:a", "queued:b", "request:2", "request:3", "idle-resolved"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// D142: an abort while the first deferred continuation runs stops that run and discards the prompts deferred behind it, so no later provider
// request starts. Pi replays the remaining deferred actions after the abort (#10705).
func TestAbortDuringADeferredContinuationDiscardsTheRest(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed atomic.Bool
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"agent_settled": {twoDeferredContinuations(&h, &armed, log)}}}
	h = newRecoveryHarness(t, harnessOptions{extension: ext},
		log.request("1", fauxReply("one", ai.StopReasonStop, 0), nil),
		log.request("2", fauxReply("two", ai.StopReasonStop, 0), func() {
			log.add("abort-requested")
			h.session.RequestAbort()
		}),
		log.request("3", fauxReply("three", ai.StopReasonStop, 0), nil))
	armed.Store(true)
	if err := h.session.Prompt(t.Context(), "first", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"request:1", "queued:a", "queued:b", "request:2", "abort-requested"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (no request after the abort)", got, want)
	}
}

// agent_settled dispatch of a custom-message run also runs without the Session lock, so a handler that awaits ctx.compact() there completes.
func TestCustomMessageRunSettledHandlerAwaitsCompaction(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed atomic.Bool
	hooks := summaryOverrideHandlers("summary from extension", nil)
	hooks["agent_settled"] = []extension.HandlerFn{func(args ...any) (any, error) {
		if !armed.CompareAndSwap(true, false) {
			return nil, nil
		}
		done := make(chan string, 2)
		h.session.ExtensionCompact(&extension.CompactOptions{
			OnComplete: func(extension.CompactionResult) { done <- "compact-complete" },
			OnError:    func(err error) { done <- "compact-error: " + err.Error() },
		})
		invocation.Acknowledge(args[1].(context.Context))
		select {
		case outcome := <-done:
			log.add(outcome)
		case <-time.After(10 * time.Second):
			log.add("compact-blocked")
		}
		return nil, nil
	}}
	h = newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: hooks}, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1}}`},
		fauxReply("warmup", ai.StopReasonStop, 0),
		log.request("custom", fauxReply("two", ai.StopReasonStop, 0), nil))
	if err := h.session.Prompt(t.Context(), "warmup", nil); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	if err := h.session.SendCustomMessage(t.Context(), extension.CustomMessageRef{CustomType: "start", Content: "START"}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
		t.Fatal(err)
	}
	want := []string{"request:custom", "compact-complete"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// Pi's compact() awaits abort(), which only stops the active run: a continuation an agent_settled handler queued before it awaits ctx.compact()
// still runs (D142 discards deferred prompts only for an explicit abort, not for manual compaction's own abort).
func TestSettledHandlerCompactionKeepsTheContinuationQueuedBeforeIt(t *testing.T) {
	log := &settledLog{}
	var h *recoveryHarness
	var armed atomic.Bool
	hooks := summaryOverrideHandlers("summary from extension", nil)
	hooks["agent_settled"] = []extension.HandlerFn{func(args ...any) (any, error) {
		if !armed.CompareAndSwap(true, false) {
			return nil, nil
		}
		if err := h.session.SendMessage(extension.CustomMessageRef{CustomType: "continue", Content: "CONTINUE"}, &extension.SendMessageOptions{TriggerTurn: new(true)}); err != nil {
			return nil, err
		}
		log.add("continuation-queued")
		done := make(chan struct{}, 2)
		h.session.ExtensionCompact(&extension.CompactOptions{
			OnComplete: func(extension.CompactionResult) { done <- struct{}{} },
			OnError:    func(error) { done <- struct{}{} },
		})
		invocation.Acknowledge(args[1].(context.Context))
		<-done
		log.add("compact-complete")
		return nil, nil
	}}
	h = newRecoveryHarness(t, harnessOptions{extension: extension.Extension{Handlers: hooks}, settings: `{"compaction":{"enabled":true,"keepRecentTokens":1}}`},
		fauxReply("warmup", ai.StopReasonStop, 0),
		log.request("1", fauxReply("two", ai.StopReasonStop, 0), nil),
		log.request("2", fauxReply("three", ai.StopReasonStop, 0), nil))
	if err := h.session.Prompt(t.Context(), "warmup", nil); err != nil {
		t.Fatal(err)
	}
	armed.Store(true)
	if err := h.session.Prompt(t.Context(), "second", nil); err != nil {
		t.Fatal(err)
	}
	if err := h.session.WaitForIdle(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := []string{"request:1", "continuation-queued", "compact-complete", "request:2"}
	if got := log.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v (manual compaction must not discard the queued continuation)", got, want)
	}
}
