package coding

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// longTurnProvider scripts one reply of n text deltas: more events than the Session's rawEvents and events buffers hold together.
func longTurnProvider(n int) staticProviderForSessionTests {
	partial := sessionTestMessage("fake", "", ai.StopReasonPending, "")
	evs := []ai.AssistantMessageEvent{ai.StartEvent{Partial: partial}}
	for index := range n {
		partial = sessionTestMessage("fake", strings.Repeat("x", index+1), ai.StopReasonPending, "")
		evs = append(evs, ai.TextDeltaEvent{ContentIndex: 0, Delta: "x", Partial: partial})
	}
	final := sessionTestMessage("fake", strings.Repeat("x", n), ai.StopReasonStop, "")
	evs = append(evs, ai.DoneEvent{Reason: ai.StopReasonStop, Message: final})
	return staticProviderForSessionTests{events: evs}
}

// sendWithin runs sess.Send in its own goroutine and reports its error, or fails the test when it does not return in time.
func sendWithin(t *testing.T, sess *Session, text string, limit time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := sess.Send(t.Context(), text)
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(limit):
		t.Fatalf("Send(%q) did not return within %v", text, limit)
		return nil
	}
}

// eventKeys identifies events by dynamic type and, for assistant message events, the text so far. Published events are copies, so comparing identity or whole values does not apply.
func eventKeys(events []agent.AgentEvent) []string {
	keys := make([]string, len(events))
	for i, ev := range events {
		keys[i] = reflect.TypeOf(ev).String()
		var message agent.AgentMessage
		switch e := ev.(type) {
		case agent.MessageStartEvent:
			message = e.Message
		case agent.MessageUpdateEvent:
			message = e.Message
		case agent.MessageEndEvent:
			message = e.Message
		}
		if message.Assistant != nil {
			keys[i] += ":" + assistantText(message.Assistant)
		}
	}
	return keys
}

// compareEvents reports the first difference between got and want by eventKeys, or "" when they are the same sequence.
func compareEvents(got, want []agent.AgentEvent) string {
	g, w := eventKeys(got), eventKeys(want)
	for i := range min(len(g), len(w)) {
		if g[i] != w[i] {
			return fmt.Sprintf("event %d: got %s, want %s", i, g[i], w[i])
		}
	}
	if len(g) != len(w) {
		return fmt.Sprintf("got %d events, want %d", len(g), len(w))
	}
	return ""
}

// recordListener records every event the Session delivers to a Subscribe listener, in order.
type recordListener struct {
	mu     sync.Mutex
	events []agent.AgentEvent
}

func (r *recordListener) listen(ev agent.AgentEvent) {
	r.mu.Lock()
	r.events = append(r.events, ev)
	r.mu.Unlock()
}

func (r *recordListener) snapshot() []agent.AgentEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]agent.AgentEvent(nil), r.events...)
}

// drainInto collects the Events channel until it closes, acknowledging FlushEvents markers as a consumer must.
func drainInto(events <-chan agent.AgentEvent, into *[]agent.AgentEvent) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if AcknowledgeEvent(ev) {
				continue
			}
			*into = append(*into, ev)
		}
	}()
	return done
}

// A program that embeds PiG and never reads Events must not hang: the quick start in doc.go does exactly that. It runs through Runtime.New, the production constructor.
func TestRuntimeSessionWithoutEventsReaderSendReturns(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()

	if err := sendWithin(t, sess, "hi", 10*time.Second); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sess.FlushEvents(t.Context()); err != nil {
		t.Fatalf("FlushEvents without a consumer: %v", err)
	}
	messages := sess.Messages()
	last := messages[len(messages)-1]
	if last.Assistant == nil || !strings.Contains(assistantText(last.Assistant), strings.Repeat("x", 300)) {
		t.Fatalf("the turn did not complete: last message %+v", last)
	}
}

// A consumer that stops reading and calls DiscardEvents releases a blocked or later turn.
func TestDiscardEventsAfterEventsStopsDelivery(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	_ = sess.Events()
	sess.DiscardEvents()
	if err := sendWithin(t, sess, "hi", 10*time.Second); err != nil {
		t.Fatalf("Send: %v", err)
	}
}

// A consumer that calls Events before Send sees every event of the turn, in the order Subscribe listeners see them.
func TestEventsBeforeSendDeliversEveryEventInOrder(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	var listened recordListener
	sess.Subscribe(listened.listen)
	var received []agent.AgentEvent
	drained := drainInto(sess.Events(), &received)

	if err := sendWithin(t, sess, "hi", 10*time.Second); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sess.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	<-drained

	want := listened.snapshot()
	if len(want) < 300 {
		t.Fatalf("listener saw %d events, want a turn longer than the buffers", len(want))
	}
	if msg := compareEvents(received, want); msg != "" {
		t.Fatalf("Events and the listener disagree: %s", msg)
	}
}

// A consumer that calls Events late misses what was emitted before, as a Pi listener subscribed late does, and then receives every later event.
func TestEventsCalledLateMissesEarlierEventsOnly(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	var listened recordListener
	sess.Subscribe(listened.listen)

	if err := sendWithin(t, sess, "first", 10*time.Second); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := sess.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	firstTurn := len(listened.snapshot())
	events := sess.Events()
	if n := len(events); n != 0 {
		t.Fatalf("%d events from before Events was called are buffered on the channel", n)
	}

	var received []agent.AgentEvent
	drained := drainInto(events, &received)
	if err := sendWithin(t, sess, "second", 10*time.Second); err != nil {
		t.Fatalf("second Send: %v", err)
	}
	if err := sess.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	<-drained

	all := listened.snapshot()
	want := all[firstTurn:]
	if len(want) == 0 || len(received) != len(want) {
		t.Fatalf("Events delivered %d events of the second turn, listener saw %d", len(received), len(want))
	}
	if msg := compareEvents(received, want); msg != "" {
		t.Fatalf("second turn: %s", msg)
	}
}

// Events called while a turn is emitting takes effect for the events sent after it, never blocks the turn, and loses nothing after that point. Run with -race: the subscription flips while the turn emits.
func TestEventsCalledDuringTurnDeliversSuffixInOrder(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	var listened recordListener
	started := make(chan struct{})
	var once sync.Once
	sess.Subscribe(func(ev agent.AgentEvent) {
		listened.listen(ev)
		if _, ok := ev.(agent.MessageUpdateEvent); ok {
			once.Do(func() { close(started) })
		}
	})

	done := make(chan error, 1)
	go func() {
		_, e := sess.Send(context.WithoutCancel(t.Context()), "hi")
		done <- e
	}()
	<-started
	var received []agent.AgentEvent
	drained := drainInto(sess.Events(), &received)
	select {
	case e := <-done:
		if e != nil {
			t.Fatalf("Send: %v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Send did not return while Events was called mid-turn")
	}
	if err := sess.FlushEvents(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
	<-drained

	all := listened.snapshot()
	if len(received) > len(all) {
		t.Fatalf("Events delivered %d of %d events", len(received), len(all))
	}
	if msg := compareEvents(received, all[len(all)-len(received):]); msg != "" {
		t.Fatalf("Events is not the listener's suffix: %s", msg)
	}
}

// A clone is a new Session with its own listeners, as a forked Pi session is: its Events channel delivers nothing until it is called there, whatever the source did.
func TestCloneEventsAreSubscribedSeparately(t *testing.T) {
	svcs := newTestServices(t)
	sess, err := NewSession(svcs, SessionOptions{Model: fakeModelWithProvider(longTurnProvider(300))})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	_ = sess.Events()
	appendUser(t, sess, "shared turn")
	appendAsst(t, sess, "shared reply")

	cloned, err := sess.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer func() { _ = cloned.Close() }()
	if err := sendWithin(t, cloned, "hi", 10*time.Second); err != nil {
		t.Fatalf("clone Send without an Events reader: %v", err)
	}
	if n := len(sess.Events()); n != 0 {
		t.Fatalf("the clone's turn put %d events on the source's channel", n)
	}
}
