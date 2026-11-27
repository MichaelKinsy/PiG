package store

import (
	"context"
	"fmt"
	"slices"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	chordtelemetry "github.com/MichaelKinsy/PiG/chord/next/telemetry"
)

// Ports packages/chord/src/store/shared-read.ts
//
// Shared live reads (decision "Identical live reads share one source"): the readers of one provider instance's named
// query with the same arguments and shape (a window of `first` rows, or a count) share one open read. One OpenQuery,
// one RegisteredRead in the store's index, one skip test, one run, one decode and one value per write, which every
// subscriber takes; the last subscriber to leave closes it. An owner's own window (`self.query`) is a read of this
// kind nobody else subscribes to.

// LiveTarget is what a live source reads: a query to keep open, and how a stored row of it becomes the value's row
// (decoded, projected, mapped); a row the code cannot read is an error.
type LiveTarget struct {
	Spec QuerySpec
	// Sharing is where its reads are shared with other readers (a named query's); nil for the owner's own window.
	Sharing *Sharing
	Decode  func(stored StoredRead, ctx context.Context) (delta.JsonValue, error)
}

// LiveShape is what a live query reads and shows: the rows of Range (Kind "rows"; Present makes the value of them and
// whether a further row exists: the rows as they are, or `{ rows, more }`), or a count (Kind "count").
type LiveShape struct {
	Kind    string
	Range   Range
	Present func(rows []delta.JsonValue, more bool) delta.JsonValue
}

// Sharing is where a named query's target is shared: its provider instance's shared reads, and its arguments' canonical
// JSON.
type Sharing struct {
	Shared *SharedReads
	Args   string
}

// ReadOutcome is what a shared read's subscribers take of a run: the value (rows as its shape presents them, or a
// count), or why not.
type ReadOutcome struct {
	Kind  string // "value" or "failed"
	Value delta.JsonValue
	Err   error
}

// ReadSubscriber is who takes a shared read's outcomes, told which read's: called inside the batch of the run's
// adoption.
type ReadSubscriber interface {
	Adopted(read *SharedRead, outcome ReadOutcome)
}

// SharedReadOf returns the shared read of target in shape: the open one of its key (the shape and the arguments), else
// a new one, whose first run is started by the caller (Open/Load) or immediately when created.
// A target nobody shares (the owner's own window) gets a read of its own, on its reader's site and context.
func SharedReadOf(site ReadSite, shape LiveShape, target LiveTarget, ctx context.Context) (*SharedRead, bool) {
	if target.Sharing == nil {
		return newSharedRead(site, ctx, shape, target, ""), true
	}
	var key string
	if shape.Kind == "rows" {
		key = fmt.Sprintf("rows %d %s", shape.Range.Limit, target.Sharing.Args)
	} else {
		key = "count " + target.Sharing.Args
	}
	return target.Sharing.Shared.of(key, site, shape, target, ctx)
}

// SharedReads holds the shared reads of one provider instance's named query, by key; kept by the query (OwnQuery), so
// they go with the provider's attempt. Their site and context are its own, made by the first: a shared read outlives
// the reader that made it, so it keeps nothing of that reader (its site wakes its attempt, its context carries its
// object, span and signal), only the store, the batch and the adapter's metrics.
type SharedReads struct {
	mu    sync.Mutex
	reads map[string]*SharedRead
	site  *ReadSite
	ctx   context.Context
}

// NewSharedReads makes an empty shared-read table for one named query instance.
func NewSharedReads() *SharedReads {
	return &SharedReads{reads: map[string]*SharedRead{}}
}

func (sr *SharedReads) of(key string, site ReadSite, shape LiveShape, target LiveTarget, ctx context.Context) (*SharedRead, bool) {
	sr.mu.Lock()
	if sr.reads == nil {
		sr.reads = map[string]*SharedRead{}
	}
	if open, ok := sr.reads[key]; ok {
		sr.mu.Unlock()
		return open, false
	}
	if sr.site == nil {
		s := ReadSite{Store: site.Store, Batch: site.Batch}
		sr.site = &s
	}
	if sr.ctx == nil {
		metrics := chordctx.Telemetry(ctx).Metrics()
		if metrics == nil {
			sr.ctx = chordctx.Background()
		} else {
			sr.ctx = chordctx.WithTelemetry(chordctx.Background(), chordtelemetry.CountingOnly(metrics))
		}
	}
	read := newSharedRead(*sr.site, sr.ctx, shape, target, key)
	sr.reads[key] = read
	sr.mu.Unlock()
	return read, true
}

func (sr *SharedReads) closed(key string) {
	sr.mu.Lock()
	delete(sr.reads, key)
	sr.mu.Unlock()
}

// SharedRead is one open read of a target in a shape, with its subscribers; the sink of its runs.
type SharedRead struct {
	Query  *OpenQuery
	read   *OpenRead
	target LiveTarget
	shape  LiveShape
	key    string
	rows   *DecodedRows

	mu          sync.Mutex
	subscribers []ReadSubscriber
	waiting     chan struct{}
	outcome     *ReadOutcome
	loading     context.CancelFunc
}

func newSharedRead(site ReadSite, ctx context.Context, shape LiveShape, target LiveTarget, key string) *SharedRead {
	sr := &SharedRead{
		target:  target,
		shape:   shape,
		key:     key,
		waiting: make(chan struct{}),
	}
	if shape.Kind == "rows" {
		sr.rows = &DecodedRows{}
	}
	sr.Query = NewOpenQuery(site, target.Spec, ReadShape(shape.Kind), sr.rebound, ctx)
	sr.Query.SetOnClose(sr.closed)
	what := ReadKind{Kind: "rows", Range: shape.Range}
	if shape.Kind == "count" {
		what = ReadKind{Kind: "count"}
	}
	sr.read = sr.Query.Read(what, sr)
	return sr
}

// StartLoad runs the read's initial load asynchronously (if not already started) and settles waiters when done.
// Callers in Go that want to run the load on a specific task or synchronously call Load() directly.
func (sr *SharedRead) Load() *ReadRun {
	run := sr.load()
	sr.settle()
	return run
}

// Subscribe registers subscriber to take outcomes from now on, delivering its current outcome at once (in a batch) when
// it has one. Returns a channel closed once the read has an outcome or its first run ended.
func (sr *SharedRead) Subscribe(subscriber ReadSubscriber, ctx context.Context) <-chan struct{} {
	sr.mu.Lock()
	added := false
	if !slices.Contains(sr.subscribers, subscriber) {
		sr.subscribers = append(sr.subscribers, subscriber)
		added = true
	}
	outcome := sr.outcome
	waiting := sr.waiting
	sr.mu.Unlock()
	if outcome != nil && added {
		_ = sr.Query.Site.Batch.Within(func() error {
			subscriber.Adopted(sr, *outcome)
			return nil
		}, ctx)
	}
	return waiting
}

// Unsubscribe removes subscriber; the last one to leave closes the read.
func (sr *SharedRead) Unsubscribe(subscriber ReadSubscriber) {
	sr.mu.Lock()
	at := slices.Index(sr.subscribers, subscriber)
	if at < 0 {
		sr.mu.Unlock()
		return
	}
	sr.subscribers = slices.Delete(sr.subscribers, at, at+1)
	empty := len(sr.subscribers) == 0
	sr.mu.Unlock()
	if !empty {
		return
	}
	sr.Query.Close()
	if sr.target.Sharing != nil {
		sr.target.Sharing.Shared.closed(sr.key)
	}
}

func (sr *SharedRead) rebound() {
	if _, settled := sr.read.failCompile(); !settled {
		sr.Query.Site.Store.Reads.Go(sr.Query.Ctx, func(ctx context.Context) {
			sr.loadWith(ctx)
		})
	}
}

func (sr *SharedRead) closed() {
	sr.mu.Lock()
	cancel := sr.loading
	sr.loading = nil
	sr.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	sr.settle()
}

// Adopt handles a run of the underlying OpenRead inside a batch: its outcome is made once and handed to every
// subscriber.
func (sr *SharedRead) Adopt(run ReadRun) {
	sr.mu.Lock()
	subs := slices.Clone(sr.subscribers)
	sr.mu.Unlock()
	if len(subs) == 0 && sr.Query.Closed() {
		return
	}
	outcome := sr.outcomeOf(run)
	sr.mu.Lock()
	sr.outcome = &outcome
	subs = slices.Clone(sr.subscribers)
	sr.mu.Unlock()
	for _, sub := range subs {
		sub.Adopted(sr, outcome)
	}
	sr.settle()
}

func (sr *SharedRead) settle() {
	sr.mu.Lock()
	ch := sr.waiting
	sr.waiting = nil
	sr.mu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (sr *SharedRead) load() *ReadRun {
	return sr.loadWith(sr.Query.Ctx)
}

func (sr *SharedRead) loadWith(base context.Context) *ReadRun {
	if sr.target.Sharing == nil {
		return sr.read.load(base)
	}
	loadCtx, cancel := context.WithCancel(base)
	sr.mu.Lock()
	prev := sr.loading
	sr.loading = cancel
	sr.mu.Unlock()
	if prev != nil {
		prev()
	}
	defer func() {
		sr.mu.Lock()
		sr.loading = nil
		sr.mu.Unlock()
		cancel()
	}()
	return sr.read.load(loadCtx)
}

func (sr *SharedRead) outcomeOf(run ReadRun) ReadOutcome {
	switch run.Kind {
	case "failed":
		return ReadOutcome{Kind: "failed", Err: run.Err}
	case "count":
		return ReadOutcome{Kind: "value", Value: float64(run.Count)}
	default:
		rows, err := sr.rows.Decode(run.Result.Rows, sr.target, sr.Query.Ctx, run.Result.IDs)
		if err != nil {
			return ReadOutcome{Kind: "failed", Err: err}
		}
		return ReadOutcome{Kind: "value", Value: sr.shape.Present(rows, run.Result.More)}
	}
}
