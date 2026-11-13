package session

// The failure, publication and abandon paths of packages/durable/src/session/writer.ts, driven on a Writer with a host the
// test scripts. Pi pins them through a whole Session (session-group-commit.test.ts: "failures per part", "a publication that
// throws", "a failure between two publications"); the Go twins of those cases pass the Session's own host, so these cases
// pin each rule of the writer where a Session cannot reach it: which outcome a part gets after a stop, what the failure
// reports as its boundary, what is published, hopped to or discarded once the Session failed, and what a panic the writer did
// not expect settles. A call is several parts: the first call (a lead part) is held in flight while the parts of the call
// under test are admitted, as upstream holds a call, so the next call is exactly those parts.

// pi: packages/durable/src/session/writer.ts

// mutation-checked (one-line mutants of writer.go, each failing the case named): the checks of the Session's failure before and
// after the call (TestWriterAFailureBeforeOrDuringTheCall...); the call's context taken from the first stored part and without its
// cancellation (TestWriterTheCallsContext...); the outcome of a part after a stop (own only for a stored part of a call that
// rejected and not after an external stop), the refusal's index, own parts, the failure's cause when the Session has not recorded
// it (TestWriterWhatAPartAfterAStopRejectsWith, ...NamesItsFirstPart...); an external failure stopping the call, its flag, and its
// parts discarded and not published (TestWriterAFailureByAnotherJob...); an adoption failure published and kept, the report of an
// observer's panic on a rejected call, the Session failed before the part resolves, the stop after it, and a failure meanwhile
// (TestWriterAPartThatFailsAtPublication); the parts admitted behind a failed call discarded once (TestWriterWhatWasAdmitted...);
// the keys leaving before Published, a key and Drained belonging to the newest unpublished part (TestWriterAPartsKeys...,
// TestWriterAKeyAndTheDrained...); abandon failing the Session, not settling a part twice, settling an unsettled one, opening a
// settled part's gate, and the Session's own failure winning (TestWriterAPanic...); a part settling once and a late waiter getting
// its outcome (TestPartSettlesOnce...); Wait joining the writer's goroutine (TestWriterWaitJoins...).
//
// equivalent mutants, not killable by any test:
//   - Publish's `at = own[0]` of the failure boundary replaced by the first part's index: when the call rejected every part before the
//     first own one is itself own (a part published or not received while the call rejects keeps its error), and a part that is not
//     own comes only after a stop or, for an external stop at the first part, with no own part at all, so own[0] is parts[0].
//   - abandon's `writer.unregister(part)` removed: a part that is settled was left by publishPart (its deferred leave) before it settled,
//     and one that is not settled is left by unpublish, so abandon's own unregister never finds a key to drop.

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/durable"
)

type reportedFailure struct {
	err error
	ctx context.Context
}

// scriptedHost is a WriterHost whose commit, hop and publication a case scripts, and that records what the writer asked of it.
type scriptedHost struct {
	mu      sync.Mutex
	failure error
	// recordFail is false for a Session that has not recorded a failure yet when the writer asks (Fail only logs).
	keepFailing bool
	fails       []fakeFail
	failCalls   int
	reports     []reportedFailure
	commits     int
	publishings int
	hops        int
	// script is the commit of every call after the first; nil receives every batch.
	script func(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error)
	// onHop runs at the n-th hop (1-based); it may fail the Session or panic.
	onHop func(n int)
	// commitCtx is the context of the n-th call.
	commitCtx []context.Context
	nextSeq   durable.Seq
	entered   chan struct{}
	gate      chan struct{}
}

func newScriptedHost() *scriptedHost {
	return &scriptedHost{entered: make(chan struct{}), gate: make(chan struct{})}
}

func (host *scriptedHost) Commit(ctx context.Context, batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) (durable.CommitResult, error) {
	host.mu.Lock()
	host.commits++
	call := host.commits
	host.commitCtx = append(host.commitCtx, ctx)
	script := host.script
	host.mu.Unlock()
	if call == 1 {
		close(host.entered)
		<-host.gate
	}
	if call > 1 && script != nil {
		return script(ctx, batches, receive)
	}
	return host.receiveAll(batches, receive), nil
}

func (host *scriptedHost) receiveAll(batches [][]durable.StorageWrite, receive func(index int, seq durable.Seq)) durable.CommitResult {
	for index := range batches {
		host.receiveOne(index, receive)
	}
	return durable.CommitResult{Stored: len(batches)}
}

func (host *scriptedHost) receiveOne(index int, receive func(index int, seq durable.Seq)) {
	host.mu.Lock()
	host.nextSeq++
	seq := host.nextSeq
	host.mu.Unlock()
	receive(index, seq)
}

func (host *scriptedHost) Stored([]durable.StorageWrite) {}

func (host *scriptedHost) Publishing(publish func(), _ context.Context) <-chan struct{} {
	host.mu.Lock()
	host.publishings++
	host.mu.Unlock()
	publish()
	return nil
}

func (host *scriptedHost) Hop() {
	host.mu.Lock()
	host.hops++
	n, onHop := host.hops, host.onHop
	host.mu.Unlock()
	if onHop != nil {
		onHop(n)
	}
}

func (host *scriptedHost) Failure() error {
	host.mu.Lock()
	defer host.mu.Unlock()
	return host.failure
}

func (host *scriptedHost) setFailure(err error) {
	host.mu.Lock()
	host.failure = err
	host.mu.Unlock()
}

func (host *scriptedHost) Fail(err error, at int, own []int) {
	host.mu.Lock()
	defer host.mu.Unlock()
	host.failCalls++
	if host.failure != nil {
		return
	}
	host.fails = append(host.fails, fakeFail{err: err, at: at, own: own})
	if !host.keepFailing {
		host.failure = err
	}
}

func (host *scriptedHost) Report(err error, ctx context.Context) {
	host.mu.Lock()
	host.reports = append(host.reports, reportedFailure{err: err, ctx: ctx})
	host.mu.Unlock()
}

// writerRig is a Writer over a scriptedHost and what its parts did.
type writerRig struct {
	t         *testing.T
	host      *scriptedHost
	writer    *Writer
	mu        sync.Mutex
	published []int
	discards  map[int]int
	// publish is what a part's Publish returns, by part index.
	publish map[int]func(durable.Seq) *PublishFailure
}

func newWriterRig(t *testing.T) *writerRig {
	t.Helper()
	host := newScriptedHost()
	return &writerRig{t: t, host: host, writer: NewWriter(host), discards: map[int]int{}, publish: map[int]func(durable.Seq) *PublishFailure{}}
}

// part is a part of line index index: stored (it writes) or held (it does not).
func (rig *writerRig) part(index int, stored bool) PartSpec {
	spec := PartSpec{
		Index: index,
		Ctx:   context.Background(),
		Publish: func(seq durable.Seq) *PublishFailure {
			rig.mu.Lock()
			rig.published = append(rig.published, index)
			publish := rig.publish[index]
			rig.mu.Unlock()
			if publish != nil {
				return publish(seq)
			}
			return nil
		},
		Discard: func() {
			rig.mu.Lock()
			rig.discards[index]++
			rig.mu.Unlock()
		},
	}
	if stored {
		spec.Writes = []durable.StorageWrite{durable.TaskWrite{}}
	}
	return spec
}

// call admits the parts as one call: a lead part (index 0) holds the writer in flight until they are all queued. It returns once
// the lead is published; the parts of the call are published or rejected when the writer ended (rig.writer.Wait).
func (rig *writerRig) call(specs ...PartSpec) []*Part {
	rig.t.Helper()
	lead := rig.writer.Admit(rig.part(0, true))
	<-rig.host.entered
	parts := make([]*Part, len(specs))
	for i, spec := range specs {
		parts[i] = rig.writer.Admit(spec)
	}
	if got := rig.writer.Queued(); got != len(specs) {
		rig.t.Fatalf("queued behind the call in flight = %d, want %d", got, len(specs))
	}
	close(rig.host.gate)
	if err := lead.Wait(); err != nil {
		rig.t.Fatalf("the lead part rejected with %v", err)
	}
	return parts
}

func (rig *writerRig) discarded(index int) int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return rig.discards[index]
}

func (rig *writerRig) publishedIndexes() []int {
	rig.mu.Lock()
	defer rig.mu.Unlock()
	return slices.Clone(rig.published)
}

// outcome waits for a part and returns its outcome; a part never settled fails the test instead of hanging it.
func outcome(part *Part) error {
	settled := make(chan error, 1)
	go func() { settled <- part.Wait() }()
	select {
	case err := <-settled:
		return err
	case <-time.After(10 * time.Second):
		panic("a part was never settled: its outcome, its gate or the close waiting on it would wait for ever")
	}
}

// sameError is Pi's `toBe`: the one error value, not one that wraps or equals it.
func sameError(got, want error) bool {
	return got == want //nolint:errorlint // identity is what Pi pins: `rejects.toBe(failure)`
}

func isFailed(err error) (error, bool) {
	failed, ok := errors.AsType[*durable.SessionFailed](err)
	if !ok {
		return nil, false
	}
	return failed.Cause, true
}

func requireSessionFailed(t *testing.T, name string, err, cause error) {
	t.Helper()
	got, ok := isFailed(err)
	if !ok {
		t.Fatalf("%s: outcome %v, want SessionFailed", name, err)
	}
	if !errors.Is(got, cause) {
		t.Fatalf("%s: SessionFailed caused by %v, want %v", name, got, cause)
	}
}

func requireOwn(t *testing.T, name string, err, want error) {
	t.Helper()
	if !sameError(err, want) {
		t.Fatalf("%s: outcome %v, want its own error %v", name, err, want)
	}
}

func requireOneFailure(t *testing.T, got []fakeFail) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("failures reported to the Session = %+v, want one", got)
	}
}

func TestWriterAFailureBeforeOrDuringTheCallSendsAndPublishesNothing(t *testing.T) {
	t.Run("a Session that failed before the call stores nothing and rejects every part with SessionFailed", func(t *testing.T) {
		rig := newWriterRig(t)
		failure := errors.New("failed before")
		rig.host.setFailure(failure)
		close(rig.host.gate) // a writer that sent anyway would not hang on the lead's gate
		stored, held := rig.writer.Admit(rig.part(1, true)), rig.writer.Admit(rig.part(2, false))
		rig.writer.Wait()
		requireSessionFailed(t, "stored", outcome(stored), failure)
		requireSessionFailed(t, "held", outcome(held), failure)
		if rig.host.commits != 0 || rig.host.publishings != 0 || len(rig.publishedIndexes()) != 0 {
			t.Fatalf("commits %d, publications %d, published %v: a Session that failed before the call must store and publish nothing", rig.host.commits, rig.host.publishings, rig.publishedIndexes())
		}
		if rig.discarded(1) != 1 || rig.discarded(2) != 1 {
			t.Fatalf("discards %v, want each part discarded once", rig.discards)
		}
	})

	t.Run("a call that settles once another failure failed the Session publishes nothing, not even through the storage's scope", func(t *testing.T) {
		rig := newWriterRig(t)
		failure := errors.New("failed meanwhile")
		rig.host.script = func(_ context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			result := rig.host.receiveAll(batches, receive)
			rig.host.setFailure(failure)
			return result, nil
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true))
		rig.writer.Wait()
		requireSessionFailed(t, "first", outcome(parts[0]), failure)
		requireSessionFailed(t, "second", outcome(parts[1]), failure)
		if got := rig.publishedIndexes(); !slices.Equal(got, []int{0}) {
			t.Fatalf("published %v, want only the lead", got)
		}
		if rig.host.publishings != 1 || rig.host.hops != 0 {
			t.Fatalf("publication scopes entered %d, hops %d: the call published nothing, so only the lead (one scope, no hop) ran", rig.host.publishings, rig.host.hops)
		}
		if rig.discarded(1) != 1 || rig.discarded(2) != 1 {
			t.Fatalf("discards %v, want each part of the call discarded once", rig.discards)
		}
	})
}

func TestWriterTheCallsContextIsTheFirstStoredPartsWithoutItsCancellation(t *testing.T) {
	type key struct{}
	rig := newWriterRig(t)
	heldCtx := context.WithValue(context.Background(), key{}, "held")
	storedCtx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "stored"))
	defer cancel()
	var seen context.Context
	var cancelledDuring error
	rig.host.script = func(ctx context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
		seen = ctx
		cancel()
		cancelledDuring = ctx.Err()
		return rig.host.receiveAll(batches, receive), nil
	}
	held, stored := rig.part(1, false), rig.part(2, true)
	held.Ctx, stored.Ctx = heldCtx, storedCtx
	parts := rig.call(held, stored)
	rig.writer.Wait()
	for i, part := range parts {
		if err := outcome(part); err != nil {
			t.Fatalf("part %d rejected with %v", i, err)
		}
	}
	if got := seen.Value(key{}); got != "stored" {
		t.Fatalf("the call carries the context of %v, want the first stored part's, not the held part before it", got)
	}
	if cancelledDuring != nil {
		t.Fatalf("the call was cancelled (%v) by its first part's context: a call carries the values of that context, never its abort signal", cancelledDuring)
	}
}

func TestWriterWhatAPartAfterAStopRejectsWith(t *testing.T) {
	t.Run("a call that rejects: the stored parts keep its error, a held part after the stop is SessionFailed", func(t *testing.T) {
		rig := newWriterRig(t)
		rejection := errors.New("storage rejected")
		rig.host.script = func(context.Context, [][]durable.StorageWrite, func(int, durable.Seq)) (durable.CommitResult, error) {
			return durable.CommitResult{}, rejection
		}
		parts := rig.call(rig.part(1, true), rig.part(2, false), rig.part(3, true))
		rig.writer.Wait()
		requireOwn(t, "stored before the held part", outcome(parts[0]), rejection)
		requireSessionFailed(t, "held after the stop", outcome(parts[1]), rejection)
		requireOwn(t, "stored after the held part", outcome(parts[2]), rejection)
	})

	t.Run("a refusal without a rejection: the parts after the refused one are SessionFailed with the refusal, and the Session fails with it at the refused part", func(t *testing.T) {
		rig := newWriterRig(t)
		refusal := errors.New("storage refused")
		rig.host.script = func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			rig.host.receiveOne(0, receive)
			return durable.CommitResult{Stored: 1, Refused: refusal}, nil
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true), rig.part(3, true))
		rig.writer.Wait()
		if err := outcome(parts[0]); err != nil {
			t.Fatalf("the part before the refused one rejected with %v", err)
		}
		requireOwn(t, "the refused part", outcome(parts[1]), refusal)
		requireSessionFailed(t, "the part after the refused one", outcome(parts[2]), refusal)
		requireOneFailure(t, rig.host.fails)
		if fail := rig.host.fails[0]; !errors.Is(fail.err, refusal) || fail.at != 2 || !slices.Equal(fail.own, []int{2}) {
			t.Fatalf("the Session failed with %+v, want the refusal at the refused part (line index 2) with only that part as its own", fail)
		}
	})

	t.Run("a refusal is the refusal of the part at its index: an earlier part that was never received was not adopted", func(t *testing.T) {
		rig := newWriterRig(t)
		refusal := errors.New("storage refused")
		rig.host.script = func(context.Context, [][]durable.StorageWrite, func(int, durable.Seq)) (durable.CommitResult, error) {
			// Nothing was received, and the refusal is of the second batch.
			return durable.CommitResult{Stored: 1, Refused: refusal}, nil
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true))
		rig.writer.Wait()
		err := outcome(parts[0])
		if err == nil || sameError(err, refusal) || err.Error() != "Storage committed without adopting the batch" {
			t.Fatalf("the first part, never received, rejected with %v, want the storage committed without adopting the batch", err)
		}
		requireSessionFailed(t, "the second part", outcome(parts[1]), err)
	})

	t.Run("the Session recorded no failure yet: a part after the stop is SessionFailed with the error that stopped the call", func(t *testing.T) {
		rig := newWriterRig(t)
		rig.host.keepFailing = true // Fail only logs: the Session has not recorded it when the outcomes are settled
		refusal := errors.New("storage refused")
		rig.host.script = func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			rig.host.receiveOne(0, receive)
			return durable.CommitResult{Stored: 1, Refused: refusal}, nil
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true), rig.part(3, true))
		rig.writer.Wait()
		requireSessionFailed(t, "the part after the refused one", outcome(parts[2]), refusal)
	})
}

func TestWriterTheFailureOfARejectedCallNamesItsFirstPartAndOnlyItsOwnParts(t *testing.T) {
	rig := newWriterRig(t)
	rejection := errors.New("storage rejected")
	rig.host.script = func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
		rig.host.receiveOne(0, receive) // the first stored part is received, the second is not
		return durable.CommitResult{}, rejection
	}
	// A held part first (published while the call rejects: durable, so it rejects with the storage error), two stored parts, and a
	// held part after the stop, which was not sent: SessionFailed, not one of the parts that keep the storage error.
	parts := rig.call(rig.part(11, false), rig.part(12, true), rig.part(13, true), rig.part(14, false))
	rig.writer.Wait()
	requireOwn(t, "held part before the stop", outcome(parts[0]), rejection)
	requireOwn(t, "received stored part", outcome(parts[1]), rejection)
	requireOwn(t, "unreceived stored part", outcome(parts[2]), rejection)
	requireSessionFailed(t, "held part after the stop", outcome(parts[3]), rejection)
	requireOneFailure(t, rig.host.fails)
	if fail := rig.host.fails[0]; !errors.Is(fail.err, rejection) || fail.at != 11 || !slices.Equal(fail.own, []int{11, 12, 13}) {
		t.Fatalf("the Session failed with %+v, want the storage error at the first part of the call that rejected (11), with parts 11, 12 and 13 as its own and not the held part 14 SessionFailed", fail)
	}
}

func TestWriterAFailureByAnotherJobBetweenTwoPublications(t *testing.T) {
	t.Run("publishes no part after it, hops no more, and fails the Session at the part it stopped at", func(t *testing.T) {
		rig := newWriterRig(t)
		failure := errors.New("failed by another job")
		rig.host.onHop = func(n int) {
			if n == 1 {
				rig.host.setFailure(failure)
			}
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true), rig.part(3, true))
		rig.writer.Wait()
		if err := outcome(parts[0]); err != nil {
			t.Fatalf("the part published before the failure rejected with %v", err)
		}
		requireSessionFailed(t, "the part reached by the failure", outcome(parts[1]), failure)
		requireSessionFailed(t, "the part after it", outcome(parts[2]), failure)
		if got := rig.publishedIndexes(); !slices.Equal(got, []int{0, 1}) {
			t.Fatalf("published %v, want the lead and the first part only", got)
		}
		if rig.host.hops != 1 || rig.host.publishings != 3 {
			t.Fatalf("hops %d, publication scopes %d: the call must stop at the part the failure reached (one hop; scopes of the lead, the first part and that part)", rig.host.hops, rig.host.publishings)
		}
		if rig.discarded(1) != 0 || rig.discarded(2) != 1 || rig.discarded(3) != 1 {
			t.Fatalf("discards %v, want the published part kept and the two after it discarded once", rig.discards)
		}
		if rig.host.failCalls != 1 {
			t.Fatalf("the writer reported the failure %d times, want once: it stopped there", rig.host.failCalls)
		}
	})

	t.Run("a call that also rejects: the parts after the stop are SessionFailed, not the storage error", func(t *testing.T) {
		rig := newWriterRig(t)
		failure := errors.New("failed by another job")
		rejection := errors.New("storage rejected")
		rig.host.onHop = func(n int) {
			if n == 1 {
				rig.host.setFailure(failure)
			}
		}
		rig.host.script = func(_ context.Context, _ [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			rig.host.receiveOne(0, receive)
			rig.host.receiveOne(1, receive)
			return durable.CommitResult{}, rejection
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true), rig.part(3, true))
		rig.writer.Wait()
		requireOwn(t, "the part published before the failure", outcome(parts[0]), rejection)
		requireSessionFailed(t, "the part the failure reached", outcome(parts[1]), failure)
		requireSessionFailed(t, "the unreceived part after it", outcome(parts[2]), failure)
	})
}

func TestWriterAPartThatFailsAtPublication(t *testing.T) {
	t.Run("an adoption that fails publishes the part (it is kept, never discarded), rejects it with the failure, and leaves the rest unpublished", func(t *testing.T) {
		rig := newWriterRig(t)
		adoption := errors.New("memory could not adopt")
		rig.publish[1] = func(durable.Seq) *PublishFailure { return &PublishFailure{Kind: PublishAdoption, Err: adoption} }
		parts := rig.call(rig.part(1, true), rig.part(2, true))
		rig.writer.Wait()
		requireOwn(t, "the part whose adoption failed", outcome(parts[0]), adoption)
		requireSessionFailed(t, "the part after it", outcome(parts[1]), adoption)
		if rig.discarded(1) != 0 || rig.discarded(2) != 1 {
			t.Fatalf("discards %v: the part whose adoption failed was published (kept), the part after it discarded once", rig.discards)
		}
		requireOneFailure(t, rig.host.fails)
		if fail := rig.host.fails[0]; !errors.Is(fail.err, adoption) || fail.at != 1 || !slices.Equal(fail.own, []int{1}) {
			t.Fatalf("the Session failed with %+v, want the adoption failure at part 1 with only it as its own", fail)
		}
	})

	t.Run("an own observer that panics on a part whose call rejects is reported, and the part rejects with the storage error", func(t *testing.T) {
		rig := newWriterRig(t)
		rejection := errors.New("storage rejected")
		listener := errors.New("own observer panicked")
		rig.host.script = func(_ context.Context, batches [][]durable.StorageWrite, receive func(int, durable.Seq)) (durable.CommitResult, error) {
			rig.host.receiveAll(batches, receive)
			return durable.CommitResult{}, rejection
		}
		rig.publish[1] = func(durable.Seq) *PublishFailure { return &PublishFailure{Kind: PublishListener, Err: listener} }
		parts := rig.call(rig.part(1, true), rig.part(2, true))
		rig.writer.Wait()
		requireOwn(t, "the part whose observer panicked", outcome(parts[0]), rejection)
		requireOwn(t, "the next part", outcome(parts[1]), rejection)
		rig.host.mu.Lock()
		reports := slices.Clone(rig.host.reports)
		rig.host.mu.Unlock()
		if len(reports) != 1 || !errors.Is(reports[0].err, listener) {
			t.Fatalf("reported %+v, want the observer's panic once", reports)
		}
	})

	t.Run("an own observer that panics fails the Session before the part resolves, and stops the call there", func(t *testing.T) {
		rig := newWriterRig(t)
		listener := errors.New("own observer panicked")
		rig.publish[1] = func(durable.Seq) *PublishFailure { return &PublishFailure{Kind: PublishListener, Err: listener} }
		var failedAtSettle error
		var settled sync.WaitGroup
		settled.Add(1)
		lead := rig.writer.Admit(rig.part(0, true))
		<-rig.host.entered
		first := rig.writer.Admit(rig.part(1, true))
		second := rig.writer.Admit(rig.part(2, true))
		first.OnSettled(func(error) {
			failedAtSettle = rig.host.Failure()
			settled.Done()
		})
		close(rig.host.gate)
		if err := lead.Wait(); err != nil {
			t.Fatal(err)
		}
		rig.writer.Wait()
		settled.Wait()
		if err := outcome(first); err != nil {
			t.Fatalf("the part whose observer panicked rejected with %v: its commit stands", err)
		}
		if !errors.Is(failedAtSettle, listener) {
			t.Fatalf("the Session had failed with %v when the part resolved, want the observer's panic: the failure comes before the commit's caller resumes", failedAtSettle)
		}
		requireSessionFailed(t, "the part after it", outcome(second), listener)
		if got := rig.publishedIndexes(); !slices.Equal(got, []int{0, 1}) {
			t.Fatalf("published %v, want the lead and the part whose observer panicked only", got)
		}
		if rig.host.hops != 0 || rig.host.publishings != 2 {
			t.Fatalf("hops %d, publication scopes %d: nothing may be published or hopped to after the failure", rig.host.hops, rig.host.publishings)
		}
		if rig.discarded(2) != 1 {
			t.Fatalf("discards %v, want the part after it discarded once", rig.discards)
		}
		requireOneFailure(t, rig.host.fails)
		if fail := rig.host.fails[0]; !errors.Is(fail.err, listener) || fail.at != 1 || len(fail.own) != 0 {
			t.Fatalf("the Session failed with %+v, want the observer's panic at part 1, no part's own", fail)
		}
	})

	t.Run("a Session that failed during the part's own publication rejects it with SessionFailed", func(t *testing.T) {
		rig := newWriterRig(t)
		closing := errors.New("closed while publishing")
		rig.publish[1] = func(durable.Seq) *PublishFailure {
			rig.host.setFailure(closing)
			return nil
		}
		parts := rig.call(rig.part(1, true))
		rig.writer.Wait()
		requireSessionFailed(t, "the part", outcome(parts[0]), closing)
	})
}

func TestWriterWhatWasAdmittedWhileACallRanIsNeverSentAfterItsFailure(t *testing.T) {
	rig := newWriterRig(t)
	rejection := errors.New("storage rejected")
	hold := make(chan struct{})
	entered := make(chan struct{})
	rig.host.script = func(context.Context, [][]durable.StorageWrite, func(int, durable.Seq)) (durable.CommitResult, error) {
		close(entered)
		<-hold
		return durable.CommitResult{}, rejection
	}
	parts := rig.call(rig.part(1, true))
	<-entered
	later := []*Part{rig.writer.Admit(rig.part(2, true)), rig.writer.Admit(rig.part(3, false))}
	if got := rig.writer.Queued(); got != 2 {
		t.Fatalf("queued behind the call in flight = %d, want 2", got)
	}
	close(hold)
	rig.writer.Wait()
	requireOwn(t, "the part of the call", outcome(parts[0]), rejection)
	for i, part := range later {
		requireSessionFailed(t, "an admitted part", outcome(part), rejection)
		if got := rig.discarded(i + 2); got != 1 {
			t.Fatalf("part %d discarded %d times, want once", i+2, got)
		}
	}
	if rig.host.commits != 2 || rig.writer.Queued() != 0 {
		t.Fatalf("calls %d, queued %d: the parts admitted behind the failed call are never sent", rig.host.commits, rig.writer.Queued())
	}
}

func TestWriterAPartsKeysLeaveBeforeItsPublishedCallbackRuns(t *testing.T) {
	rig := newWriterRig(t)
	var gate <-chan struct{}
	var unpublished int
	spec := rig.part(1, true)
	spec.Keys = []string{"k"}
	spec.Published = func() {
		gate = rig.writer.Gate("k")
		unpublished = rig.writer.Unpublished()
	}
	parts := rig.call(spec)
	rig.writer.Wait()
	if err := outcome(parts[0]); err != nil {
		t.Fatal(err)
	}
	if gate != nil || unpublished != 0 {
		t.Fatalf("inside Published the key still has a writer (gate %v, %d unpublished keys): its keys leave first, so what Published schedules sees it published", gate, unpublished)
	}
}

func TestWriterAKeyAndTheDrainedGateBelongToTheNewestUnpublishedPart(t *testing.T) {
	rig := newWriterRig(t)
	var older, newer *Part
	var gateInOlder, drainedInNewer <-chan struct{}
	oldSpec := rig.part(1, true)
	oldSpec.Keys = []string{"k"}
	oldSpec.Published = func() { gateInOlder = rig.writer.Gate("k") }
	newSpec := rig.part(2, true)
	newSpec.Keys = []string{"k"}
	// The newer part's publication runs after the older part opened its gate.
	rig.publish[2] = func(durable.Seq) *PublishFailure {
		drainedInNewer = rig.writer.Drained()
		return nil
	}
	older = rig.writer.Admit(oldSpec)
	<-rig.host.entered
	newer = rig.writer.Admit(newSpec)
	close(rig.host.gate)
	if err := older.Wait(); err != nil {
		t.Fatal(err)
	}
	rig.writer.Wait()
	if err := newer.Wait(); err != nil {
		t.Fatal(err)
	}
	// While the older part was being published the newer one was admitted and unpublished: it holds the key and the drained gate.
	if gateInOlder == nil || gateInOlder != (<-chan struct{})(newer.gate) {
		t.Fatal("the older part's publication dropped the key the newer unpublished part also writes: a key leaves when its newest writer is published")
	}
	if drainedInNewer == nil || drainedInNewer != (<-chan struct{})(newer.gate) {
		t.Fatal("the older part opening its gate cleared the newest part: Drained must stay the newest unpublished part's gate until it is published")
	}
	if rig.writer.Gate("k") != nil || rig.writer.Drained() != nil || rig.writer.Unpublished() != 0 {
		t.Fatal("after both are published the key and the drained gate must be gone")
	}
}

func TestWriterAPanicTheWriterDidNotExpectFailsTheSessionAndSettlesTheParts(t *testing.T) {
	t.Run("in a hop between two parts: the published part keeps its outcome, the rest are SessionFailed and discarded once", func(t *testing.T) {
		rig := newWriterRig(t)
		unexpected := errors.New("unexpected")
		rig.host.onHop = func(int) { panic(unexpected) }
		parts := rig.call(rig.part(21, true), rig.part(22, true))
		rig.writer.Wait()
		if err := outcome(parts[0]); err != nil {
			t.Fatalf("the part published before the panic rejected with %v", err)
		}
		requireSessionFailed(t, "the part after it", outcome(parts[1]), unexpected)
		if rig.discarded(21) != 0 || rig.discarded(22) != 1 {
			t.Fatalf("discards %v, want the settled part untouched and the other discarded once", rig.discards)
		}
		requireOneFailure(t, rig.host.fails)
		if fail := rig.host.fails[0]; !errors.Is(fail.err, unexpected) || fail.at != 21 || len(fail.own) != 0 {
			t.Fatalf("the Session failed with %+v, want the panic at the first part of the call (21), no part's own", fail)
		}
		if rig.writer.Unpublished() != 0 || rig.writer.Drained() != nil {
			t.Fatal("a key or the drained gate was stranded")
		}
	})

	t.Run("in the call itself: the parts are SessionFailed with the failure the Session recorded first", func(t *testing.T) {
		rig := newWriterRig(t)
		first, unexpected := errors.New("recorded first"), errors.New("unexpected")
		rig.host.script = func(context.Context, [][]durable.StorageWrite, func(int, durable.Seq)) (durable.CommitResult, error) {
			rig.host.setFailure(first)
			panic(unexpected)
		}
		parts := rig.call(rig.part(1, true), rig.part(2, true))
		rig.writer.Wait()
		requireSessionFailed(t, "first", outcome(parts[0]), first)
		requireSessionFailed(t, "second", outcome(parts[1]), first)
	})

	t.Run("in a waiter of a part that settled: the settled part still opens its gate, the next is SessionFailed", func(t *testing.T) {
		rig := newWriterRig(t)
		lead := rig.writer.Admit(rig.part(0, true))
		<-rig.host.entered
		first := rig.writer.Admit(rig.part(1, true))
		second := rig.writer.Admit(rig.part(2, true))
		unexpected := errors.New("waiter panicked")
		first.OnSettled(func(error) { panic(unexpected) })
		close(rig.host.gate)
		if err := lead.Wait(); err != nil {
			t.Fatal(err)
		}
		rig.writer.Wait()
		if err := outcome(first); err != nil {
			t.Fatalf("the settled part rejected with %v", err)
		}
		requireSessionFailed(t, "the part after it", outcome(second), unexpected)
		select {
		case <-first.gate:
		default:
			t.Fatal("the gate of the part whose waiter panicked stayed closed: a job waiting for it would wait for ever")
		}
		if rig.writer.Drained() != nil {
			t.Fatal("the drained gate of the newest part was stranded")
		}
	})
}

func TestWriterWaitJoinsTheGoroutineThatSendsAndPublishes(t *testing.T) {
	rig := newWriterRig(t)
	var waiting atomic.Bool
	part := rig.writer.Admit(rig.part(1, true))
	<-rig.host.entered
	go func() {
		for !waiting.Load() {
			runtime.Gosched()
		}
		// What follows the call in the writer's goroutine is slower than a return of Wait that joins nothing.
		time.Sleep(20 * time.Millisecond)
		close(rig.host.gate)
	}()
	waiting.Store(true)
	rig.writer.Wait()
	if !part.isSettled() {
		t.Fatal("Wait returned while a call was still in flight: it must join the writer's goroutine")
	}
	rig.writer.mu.Lock()
	flight := rig.writer.flight
	rig.writer.mu.Unlock()
	if flight {
		t.Fatal("Wait returned with the writer still in flight")
	}
}

func TestPartSettlesOnceAndReportsItsOutcomeToALateWaiter(t *testing.T) {
	newPart := func() *Part { return &Part{outcome: make(chan struct{}), gate: make(chan struct{})} }
	first, second := errors.New("first"), errors.New("second")

	t.Run("the first settlement wins, as a promise settles once", func(t *testing.T) {
		part := newPart()
		var got []error
		part.OnSettled(func(err error) { got = append(got, err) })
		part.settle(first)
		part.settle(second)
		if err := part.Wait(); !errors.Is(err, first) {
			t.Fatalf("Wait = %v, want the first settlement %v", err, first)
		}
		if len(got) != 1 || !errors.Is(got[0], first) {
			t.Fatalf("a waiter ran with %v, want once with %v", got, first)
		}
	})

	t.Run("a waiter that comes after the settlement runs at once with the outcome", func(t *testing.T) {
		part := newPart()
		part.settle(first)
		var got []error
		part.OnSettled(func(err error) { got = append(got, err) })
		if len(got) != 1 || !errors.Is(got[0], first) {
			t.Fatalf("a late waiter ran with %v, want once with %v", got, first)
		}
	})
}
