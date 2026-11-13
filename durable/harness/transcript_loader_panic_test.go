package harness

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/durable"
)

// A load is registered while its loader runs, and the calls that arrive meanwhile wait for it. Upstream's loader runs inside
// the promise they share, so a loader that throws rejects them all and clears the load (transcript.ts loadEarlier: "even its
// synchronous throw settles a promise that is already shared"). A Go loader that panics unwinds through the call that
// registered the load: that call must end the load on its way out, or it stays registered and every later call waits for it.
func TestConversationTranscriptLoadsAgainAfterItsLoaderPanicked(t *testing.T) {
	all := transcriptEntries(100)
	inner := &countingLoader{entries: all}
	var broken atomic.Bool
	broken.Store(true)
	load := func(ctx context.Context, query durable.EntryQuery, limit int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		if broken.Load() {
			panic("the loader's own bug")
		}
		return inner.load(ctx, query, limit)
	}
	helper := newTranscript(t, load)
	helper.Merge(windowOf(all, 40, 49))
	stalled := helper.Merge(windowOf(all, 90, 99))
	gap := firstGap(t, stalled)
	panicked := func(call func() (*TranscriptView, error)) (recovered any) {
		defer func() { recovered = recover() }()
		_, _ = call()
		return nil
	}
	if panicked(func() (*TranscriptView, error) { return helper.LoadEarlier(testContext) }) == nil {
		t.Fatal("loadEarlier: the loader's panic did not reach its caller")
	}
	if panicked(func() (*TranscriptView, error) { return helper.FillGap(testContext, gap) }) == nil {
		t.Fatal("fillGap: the loader's panic did not reach its caller")
	}
	broken.Store(false)
	// A call that finds a load registered only waits for it, and gives up at once with a cancelled context; a call that finds
	// none runs the loader, which does not read the context.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := helper.LoadEarlier(cancelled); err != nil {
		t.Fatalf("loadEarlier after a load whose loader panicked: %v: that load is still registered, and it never ends", err)
	}
	if _, err := helper.FillGap(cancelled, gap); err != nil {
		t.Fatalf("fillGap after a load whose loader panicked: %v: that load is still registered, and it never ends", err)
	}
	if calls := inner.calls.Load(); calls != 2 {
		t.Fatalf("%d loads after the panics, want 2", calls)
	}
}

// The callers that share a load whose loader panics get an error; they do not wait for good.
func TestConversationTranscriptFailsTheCallersSharingALoadWhoseLoaderPanicked(t *testing.T) {
	all := transcriptEntries(30)
	entered, gate := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	load := func(context.Context, durable.EntryQuery, int) (durable.Page[durable.EntryRecord, durable.Cursor], error) {
		if once.CompareAndSwap(false, true) {
			close(entered)
		}
		<-gate
		panic("the loader's own bug")
	}
	helper := newTranscript(t, load)
	helper.Merge(windowOf(all, 20, 29))
	call := func() (view *TranscriptView, err error) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("panic: %v", recovered)
			}
		}()
		return helper.LoadEarlier(testContext)
	}
	first := startPending(call)
	<-entered
	// It shares the first call's load when it arrives while the loader is held, and runs the loader itself otherwise: either way it ends.
	second := startPending(call)
	close(gate)
	<-first.done
	<-second.done
	if first.err == nil || second.err == nil {
		t.Fatalf("first %v, second %v: want both to fail", first.err, second.err)
	}
	if first.err.Error() != "panic: the loader's own bug" {
		t.Fatalf("first: %v, want the loader's panic", first.err)
	}
	if second.err.Error() != "ConversationTranscript: the entry loader panicked" && second.err.Error() != "panic: the loader's own bug" {
		t.Fatalf("second: %v", second.err)
	}
}
