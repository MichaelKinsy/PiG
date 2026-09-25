package agentharness

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent/harness"
)

func runStart(runID, lane string) HarnessEvent {
	return HarnessEvent{Payload: RunStartPayload{RunID: runID, StartedAt: 1}, Lane: lane}
}

func queueUpdate(entryID string) HarnessEvent {
	return HarnessEvent{Payload: QueueUpdatePayload{Queues: []LaneQueuedItem{{EntryID: entryID, Kind: "nextRun", Type: "message", Message: userMessage(entryID, 2)}}}, Lane: "main"}
}

type recorder struct {
	mu   sync.Mutex
	seen []string
}

func (r *recorder) add(value string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, value)
}

func (r *recorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.seen)
}

func (r *recorder) waitFor(t *testing.T, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if slices.Equal(r.get(), want) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("seen = %v, want %v", r.get(), want)
}

func mustSubscribe(t *testing.T) func(unsubscribe func(), err error) func() {
	return func(unsubscribe func(), err error) func() {
		t.Helper()
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		return unsubscribe
	}
}

// Upstream: buffers between snapshot and start, then delivers each event once
// in order.
func TestHarnessEventBusWatcherBuffersUntilStart(t *testing.T) {
	bus := NewHarnessEventBus()
	type snapshot struct{ Tip string }
	watcher, err := Watch(bus, snapshot{}, func(HarnessEvent) bool { return true }, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bus.Emit(ctx, runStart("one", "main"))
	seen := &recorder{}
	if err := watcher.Start(func(_ harness.Context, event HarnessEvent) error {
		seen.add(string(event.Type()) + ":" + event.Payload.(RunStartPayload).RunID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(ctx, runStart("two", "main"))
	seen.waitFor(t, []string{"run_start:one", "run_start:two"})
	if watcher.Snapshot() != (snapshot{}) {
		t.Fatalf("snapshot changed: %+v", watcher.Snapshot())
	}
	if err := watcher.Start(func(harness.Context, HarnessEvent) error { return nil }); err == nil {
		t.Fatal("second Start succeeded")
	}
	watcher.Unsubscribe()
	watcher.Unsubscribe()
	bus.Emit(ctx, runStart("three", "main"))
	time.Sleep(10 * time.Millisecond)
	if got := seen.get(); len(got) != 2 {
		t.Fatalf("delivered after unsubscribe: %v", got)
	}
	if _, err := watcher.Resnapshot(ctx); err == nil || err.Error() != "WatchHandle is unsubscribed" {
		t.Fatalf("resnapshot after unsubscribe = %v", err)
	}
}

// Upstream: drops pre-snapshot delivery and holds later events when
// resnapshotting inside a listener.
func TestHarnessEventBusResnapshotInsideListener(t *testing.T) {
	bus := NewHarnessEventBus()
	listenerStarted, releaseListener, resnapshotDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var watcher *BufferedEventWatcher[string]
	watcher, err := Watch(bus, "old", func(HarnessEvent) bool { return true }, func(_ harness.Context, markBoundary func() error) (string, error) {
		if err := markBoundary(); err != nil {
			return "", err
		}
		return "fresh", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	queueEvents := &recorder{}
	resnapshotErr := make(chan error, 1)
	if err := watcher.Start(func(ctx harness.Context, event HarnessEvent) error {
		switch payload := event.Payload.(type) {
		case RunStartPayload:
			if payload.RunID == "blocking" {
				close(listenerStarted)
				<-releaseListener
			}
		case NavigationEndPayload:
			_, err := watcher.Resnapshot(ctx)
			resnapshotErr <- err
			close(resnapshotDone)
		case QueueUpdatePayload:
			queueEvents.add(payload.Queues[0].EntryID)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	bus.Emit(ctx, runStart("blocking", "main"))
	<-listenerStarted
	bus.EmitBatch(ctx, []HarnessEvent{
		{Payload: NavigationEndPayload{RunID: "navigation", Status: "completed", EndedAt: 2}, Lane: "main"},
		queueUpdate("stale"),
	})
	close(releaseListener)
	<-resnapshotDone
	if err := <-resnapshotErr; err != nil {
		t.Fatal(err)
	}
	if watcher.Snapshot() != "fresh" {
		t.Fatalf("snapshot = %q, want fresh", watcher.Snapshot())
	}
	if got := queueEvents.get(); len(got) != 0 {
		t.Fatalf("stale events delivered: %v", got)
	}
	bus.Emit(ctx, queueUpdate("later"))
	queueEvents.waitFor(t, []string{"later"})
}

// A resnapshot capture must mark its boundary exactly once.
func TestHarnessEventBusResnapshotBoundaryRules(t *testing.T) {
	bus := NewHarnessEventBus()
	unmarked, err := Watch(bus, 0, func(HarnessEvent) bool { return true }, func(harness.Context, func() error) (int, error) { return 1, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unmarked.Resnapshot(context.Background()); err == nil || err.Error() != "Resnapshot capture did not mark its boundary" {
		t.Fatalf("unmarked resnapshot = %v", err)
	}
	twice, _ := Watch(bus, 0, func(HarnessEvent) bool { return true }, func(_ harness.Context, mark func() error) (int, error) {
		if err := mark(); err != nil {
			return 0, err
		}
		return 2, mark()
	})
	if _, err := twice.Resnapshot(context.Background()); err == nil || err.Error() != "Resnapshot boundary was already marked" {
		t.Fatalf("double-marked resnapshot = %v", err)
	}
	if twice.Snapshot() != 0 {
		t.Fatalf("failed resnapshot replaced the snapshot: %d", twice.Snapshot())
	}
	plain, _ := Watch(bus, 0, func(HarnessEvent) bool { return true }, nil)
	if _, err := plain.Resnapshot(context.Background()); err == nil || err.Error() != "WatchHandle does not support resnapshot" {
		t.Fatalf("resnapshot without capture = %v", err)
	}
}

func TestHarnessEventBusWatchFromSnapshot(t *testing.T) {
	bus := NewHarnessEventBus()
	captures := 0
	failure := errors.New("capture failed")
	if _, err := WatchFromSnapshot(context.Background(), bus, func(harness.Context) (int, error) { return 0, failure }, func(HarnessEvent) bool { return true }); err != failure { //nolint:errorlint // exact capture error identity.
		t.Fatalf("failed capture = %v", err)
	}
	bus.mu.Lock()
	if len(bus.watchListeners) != 0 {
		t.Fatalf("failed capture left %d watchers installed", len(bus.watchListeners))
	}
	bus.mu.Unlock()
	watcher, err := WatchFromSnapshot(context.Background(), bus, func(harness.Context) (int, error) {
		captures++
		return captures, nil
	}, func(HarnessEvent) bool { return true })
	if err != nil || watcher.Snapshot() != 1 {
		t.Fatalf("watch = %v, %v", watcher, err)
	}
	next, err := watcher.Resnapshot(context.Background())
	if err != nil || next != 2 || watcher.Snapshot() != 2 {
		t.Fatalf("resnapshot = %d, %v", next, err)
	}
}

// Upstream: isolates each listener from payload mutation.
func TestHarnessEventBusIsolatesPayloadMutation(t *testing.T) {
	bus := NewHarnessEventBus()
	var observed []string
	mustSubscribe(t)(bus.On(EventConfigUpdate, func(_ harness.Context, event HarnessEvent) error {
		payload := event.Payload.(ConfigUpdatePayload)
		if payload.Property == ConfigActiveTools {
			tools := payload.Value.([]string)
			tools[0] = "mutated"
		}
		return nil
	}))
	mustSubscribe(t)(bus.On(EventConfigUpdate, func(_ harness.Context, event HarnessEvent) error {
		observed = event.Payload.(ConfigUpdatePayload).Value.([]string)
		return nil
	}))
	emitted := []string{"read"}
	bus.Emit(context.Background(), HarnessEvent{Payload: ConfigUpdatePayload{Property: ConfigActiveTools, Value: emitted, Previous: []string{}}, Lane: "main"})
	if !slices.Equal(observed, []string{"read"}) || !slices.Equal(emitted, []string{"read"}) {
		t.Fatalf("observed %v emitted %v, want isolated [read]", observed, emitted)
	}
}

// Upstream: serializes concurrent publications in process order.
func TestHarnessEventBusSerializesPublications(t *testing.T) {
	bus := NewHarnessEventBus()
	started, release := make(chan struct{}), make(chan struct{})
	seen := &recorder{}
	mustSubscribe(t)(bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		runID := event.Payload.(RunStartPayload).RunID
		seen.add(runID + ":start")
		if runID == "one" {
			close(started)
			<-release
		}
		seen.add(runID + ":end")
		return nil
	}))
	var wg sync.WaitGroup
	wg.Go(func() { bus.Emit(context.Background(), runStart("one", "main")) })
	<-started
	before := currentTail(bus)
	wg.Go(func() { bus.Emit(context.Background(), runStart("two", "main")) })
	waitTailAdvance(t, bus, before)
	time.Sleep(10 * time.Millisecond)
	if got := seen.get(); !slices.Equal(got, []string{"one:start"}) {
		t.Fatalf("seen before release = %v", got)
	}
	close(release)
	wg.Wait()
	if got := seen.get(); !slices.Equal(got, []string{"one:start", "one:end", "two:start", "two:end"}) {
		t.Fatalf("seen = %v", got)
	}
}

type sourceKey struct{}

// Upstream: keeps concurrent batches contiguous with their emitting contexts.
func TestHarnessEventBusBatchesStayContiguous(t *testing.T) {
	bus := NewHarnessEventBus()
	first := context.WithValue(context.Background(), sourceKey{}, "first")
	second := context.WithValue(context.Background(), sourceKey{}, "second")
	seen := &recorder{}
	mustSubscribe(t)(bus.On(EventRunStart, func(ctx harness.Context, event HarnessEvent) error {
		seen.add(event.Payload.(RunStartPayload).RunID + "@" + ctx.Value(sourceKey{}).(string))
		time.Sleep(time.Millisecond)
		return nil
	}))
	var wg sync.WaitGroup
	before := currentTail(bus)
	wg.Go(func() {
		bus.EmitBatch(first, []HarnessEvent{runStart("a1", "main"), runStart("a2", "main")})
	})
	waitTailAdvance(t, bus, before)
	wg.Go(func() {
		bus.EmitBatch(second, []HarnessEvent{runStart("b1", "worker"), runStart("b2", "worker")})
	})
	wg.Wait()
	if got := seen.get(); !slices.Equal(got, []string{"a1@first", "a2@first", "b1@second", "b2@second"}) {
		t.Fatalf("seen = %v", got)
	}
}

// Upstream: binds listeners and watchers when a batch is emitted.
func TestHarnessEventBusBindsRecipientsAtEmit(t *testing.T) {
	bus := NewHarnessEventBus()
	started, release := make(chan struct{}), make(chan struct{})
	mustSubscribe(t)(bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		if event.Payload.(RunStartPayload).RunID == "blocking" {
			close(started)
			<-release
		}
		return nil
	}))
	var wg sync.WaitGroup
	wg.Go(func() { bus.Emit(context.Background(), runStart("blocking", "main")) })
	<-started
	before := currentTail(bus)
	wg.Go(func() { bus.Emit(context.Background(), runStart("queued", "main")) })
	waitTailAdvance(t, bus, before)
	lateListener := &recorder{}
	mustSubscribe(t)(bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		lateListener.add(event.Payload.(RunStartPayload).RunID)
		return nil
	}))
	lateWatcherEvents := &recorder{}
	lateWatcher, err := Watch(bus, struct{}{}, func(event HarnessEvent) bool { return event.Type() == EventRunStart }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := lateWatcher.Start(func(_ harness.Context, event HarnessEvent) error {
		lateWatcherEvents.add(event.Payload.(RunStartPayload).RunID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if len(lateListener.get()) != 0 || len(lateWatcherEvents.get()) != 0 {
		t.Fatalf("late recipients saw earlier batches: %v %v", lateListener.get(), lateWatcherEvents.get())
	}
	bus.Emit(context.Background(), runStart("later", "main"))
	if got := lateListener.get(); !slices.Equal(got, []string{"later"}) {
		t.Fatalf("late listener = %v", got)
	}
	lateWatcherEvents.waitFor(t, []string{"later"})
}

func currentTail(bus *HarnessEventBus) chan struct{} {
	bus.mu.Lock()
	defer bus.mu.Unlock()
	return bus.tail
}

// waitTailAdvance waits until another batch has bound its recipients and
// taken its place in the delivery tail.
func waitTailAdvance(t *testing.T, bus *HarnessEventBus, before chan struct{}) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for currentTail(bus) == before {
		if time.Now().After(deadline) {
			t.Fatal("batch was not queued")
		}
		time.Sleep(100 * time.Microsecond)
	}
}

// Upstream: resolves an empty batch without delivery.
func TestHarnessEventBusEmptyBatch(t *testing.T) {
	bus := NewHarnessEventBus()
	calls := 0
	mustSubscribe(t)(bus.On(EventRunStart, func(harness.Context, HarnessEvent) error { calls++; return nil }))
	bus.EmitBatch(context.Background(), nil)
	if calls != 0 {
		t.Fatalf("empty batch delivered %d events", calls)
	}
}

// Upstream: drains already emitted contiguous batches during close and
// ignores later publication.
func TestHarnessEventBusCloseDrainsBoundBatches(t *testing.T) {
	bus := NewHarnessEventBus()
	started, release := make(chan struct{}), make(chan struct{})
	seen := &recorder{}
	mustSubscribe(t)(bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		runID := event.Payload.(RunStartPayload).RunID
		seen.add(runID)
		if runID == "blocking" {
			close(started)
			<-release
		}
		return nil
	}))
	var wg sync.WaitGroup
	wg.Go(func() { bus.Emit(context.Background(), runStart("blocking", "main")) })
	<-started
	before := currentTail(bus)
	wg.Go(func() {
		bus.EmitBatch(context.Background(), []HarnessEvent{runStart("one", "main"), runStart("two", "main")})
	})
	waitTailAdvance(t, bus, before)
	closedErr := errors.New("closed")
	bus.Close(closedErr)
	bus.Emit(context.Background(), runStart("late", "main"))
	close(release)
	wg.Wait()
	if got := seen.get(); !slices.Equal(got, []string{"blocking", "one", "two"}) {
		t.Fatalf("seen = %v", got)
	}
	if _, err := bus.On(EventRunStart, func(harness.Context, HarnessEvent) error { return nil }); err != closedErr { //nolint:errorlint // exact close error identity.
		t.Fatalf("On after close = %v", err)
	}
	if _, err := Watch(bus, 0, func(HarnessEvent) bool { return true }, nil); err != closedErr { //nolint:errorlint // exact close error identity.
		t.Fatalf("Watch after close = %v", err)
	}
}

// Upstream: continues watcher delivery after listener failure and reports it.
func TestHarnessEventBusWatcherFailureReported(t *testing.T) {
	bus := NewHarnessEventBus()
	failures := &recorder{}
	mustSubscribe(t)(bus.On(EventHandlerError, func(_ harness.Context, event HarnessEvent) error {
		payload := event.Payload.(HandlerErrorPayload)
		failures.add(payload.Error + "|" + payload.Kind + "|" + payload.Event + "|" + event.Lane)
		return nil
	}))
	watcher, err := Watch(bus, struct{}{}, func(event HarnessEvent) bool { return event.Type() == EventRunStart }, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := &recorder{}
	if err := watcher.Start(func(_ harness.Context, event HarnessEvent) error {
		runID := event.Payload.(RunStartPayload).RunID
		if runID == "one" {
			return errors.New("watcher failed")
		}
		seen.add(runID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bus.Emit(context.Background(), runStart("one", "main"))
	bus.Emit(context.Background(), runStart("two", "main"))
	seen.waitFor(t, []string{"two"})
	failures.waitFor(t, []string{"watcher failed|event|run_start|main"})
}

// Listener failures become handler_error events; handler_error listener
// failures are dropped rather than reported recursively.
func TestHarnessEventBusListenerFailureIsolation(t *testing.T) {
	bus := NewHarnessEventBus()
	reported := &recorder{}
	mustSubscribe(t)(bus.On(EventHandlerError, func(_ harness.Context, event HarnessEvent) error {
		reported.add(event.Payload.(HandlerErrorPayload).Error)
		return errors.New("handler_error listener failed")
	}))
	later := &recorder{}
	mustSubscribe(t)(bus.On(EventFault, func(harness.Context, HarnessEvent) error { return errors.New("fault listener failed") }))
	mustSubscribe(t)(bus.On(EventFault, func(_ harness.Context, event HarnessEvent) error {
		later.add(event.Payload.(FaultPayload).Code)
		return nil
	}))
	bus.Emit(context.Background(), HarnessEvent{Payload: FaultPayload{Code: "boom", Message: "m"}})
	if got := reported.get(); !slices.Equal(got, []string{"fault listener failed"}) {
		t.Fatalf("reported = %v", got)
	}
	if got := later.get(); !slices.Equal(got, []string{"boom"}) {
		t.Fatalf("later listener = %v", got)
	}
}

func TestHarnessEventJSONFlattensEnvelope(t *testing.T) {
	data, err := json.Marshal(HarnessEvent{Payload: RunStartPayload{RunID: "r", StartedAt: 5}, Lane: "main", Recovery: true})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["type"] != "run_start" || fields["lane"] != "main" || fields["recovery"] != true || fields["runId"] != "r" || fields["startedAt"] != float64(5) {
		t.Fatalf("json = %s", data)
	}
	data, _ = json.Marshal(HarnessEvent{Payload: FaultPayload{Code: "c", Message: "m"}})
	if string(data) != `{"code":"c","message":"m","type":"fault"}` {
		t.Fatalf("global json = %s", data)
	}
}

// The payload is cloned when the batch is bound, so emitter mutation while the
// batch waits in the tail is not observed (upstream structuredClone at emit).
func TestHarnessEventBusClonesAtEmit(t *testing.T) {
	bus := NewHarnessEventBus()
	started, release := make(chan struct{}), make(chan struct{})
	var observed []string
	mustSubscribe(t)(bus.On(EventRunStart, func(harness.Context, HarnessEvent) error {
		close(started)
		<-release
		return nil
	}))
	mustSubscribe(t)(bus.On(EventConfigUpdate, func(_ harness.Context, event HarnessEvent) error {
		observed = event.Payload.(ConfigUpdatePayload).Value.([]string)
		return nil
	}))
	var wg sync.WaitGroup
	wg.Go(func() { bus.Emit(context.Background(), runStart("blocking", "main")) })
	<-started
	tools := []string{"read"}
	before := currentTail(bus)
	wg.Go(func() {
		bus.Emit(context.Background(), HarnessEvent{Payload: ConfigUpdatePayload{Property: ConfigActiveTools, Value: tools}, Lane: "main"})
	})
	waitTailAdvance(t, bus, before)
	tools[0] = "mutated after emit"
	close(release)
	wg.Wait()
	if !slices.Equal(observed, []string{"read"}) {
		t.Fatalf("observed = %v, want the emitted value", observed)
	}
}

// Upstream catches listener throws; a Go panic is the equivalent. A panicking
// listener is reported as handler_error and later listeners still run.
func TestHarnessEventBusIsolatesListenerPanics(t *testing.T) {
	bus := NewHarnessEventBus()
	reported := &recorder{}
	mustSubscribe(t)(bus.On(EventHandlerError, func(_ harness.Context, event HarnessEvent) error {
		reported.add(event.Payload.(HandlerErrorPayload).Error)
		return nil
	}))
	mustSubscribe(t)(bus.On(EventRunStart, func(harness.Context, HarnessEvent) error { panic("listener exploded") }))
	later := &recorder{}
	mustSubscribe(t)(bus.On(EventRunStart, func(_ harness.Context, event HarnessEvent) error {
		later.add(event.Payload.(RunStartPayload).RunID)
		return nil
	}))
	watcher, err := Watch(bus, 0, func(event HarnessEvent) bool { return event.Type() == EventRunStart }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := watcher.Start(func(harness.Context, HarnessEvent) error { panic(errors.New("watcher exploded")) }); err != nil {
		t.Fatal(err)
	}
	bus.Emit(context.Background(), runStart("r", "main"))
	if got := later.get(); !slices.Equal(got, []string{"r"}) {
		t.Fatalf("later listener = %v", got)
	}
	reported.waitFor(t, []string{"listener exploded", "watcher exploded"})
}
