package telemetryingest

// Ports packages/chord/src/telemetry-ingest/sender.ts (Pi main c23b9ec1). How a page and a worker send what their recorder
// kept (decisions "Telemetry from browser pages", "The server is the one place telemetry is stored"): their host holds the
// server's telemetry object over the link it has, and each send takes one batch from the recorder and calls `ingest` with
// it; a batch that cannot be sent (no link, a refusal) is given up, best effort. One send at a time; nothing is sent while
// the object is not bound.
//
// Forced Go difference (D454): Send blocks until its batch was answered (upstream returns a promise), and calls are
// serialised by a mutex, so the chain of promises is a lock.

import (
	"context"
	"sync"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Sender is a running sender: Send sends one batch now (with partial, the metric intervals not over too), Stop stops.
type Sender struct {
	recorder *Recorder
	limit    int
	waiting  func(partial bool) bool
	release  func()
	call     func(batch TelemetryBatch) (sent bool, err error)

	mu      sync.Mutex
	stopped bool
}

// SendPageTelemetry sends a page's spans (never its metrics) to the server's PageTelemetry, at most PageBatchSpans a
// batch.
func SendPageTelemetry(h host.Host, recorder *Recorder, ctx context.Context) (*Sender, error) {
	held, err := h.Hold(ctx, PageTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &Sender{
		recorder: recorder, limit: PageBatchSpans,
		// Spans only: its metrics are taken with them and dropped.
		waiting: func(bool) bool { return recorder.QueuedSpans() > 0 },
		release: held.Release,
		call: func(batch TelemetryBatch) (bool, error) {
			if held.State().Value().Kind != host.RefStateLive {
				return false, nil
			}
			_, err := held.Call("ingest", delta.JsonObjectOf("spans", encodeSpans(batch.Spans)), chordctx.Background())
			return true, err
		},
	}, nil
}

// SendWorkerTelemetry sends a worker's spans and metrics to the server's WorkerTelemetry, at most WorkerBatchRows spans a
// batch.
func SendWorkerTelemetry(h host.Host, recorder *Recorder, ctx context.Context) (*Sender, error) {
	held, err := h.Hold(ctx, WorkerTelemetry, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &Sender{
		recorder: recorder, limit: WorkerBatchRows,
		waiting: recorder.Pending,
		release: held.Release,
		call: func(batch TelemetryBatch) (bool, error) {
			if held.State().Value().Kind != host.RefStateLive {
				return false, nil
			}
			if variant, _, _ := held.Variant(); variant != "worker" {
				return false, nil
			}
			// A batch takes at most WorkerBatchRows metric rows; more series in one interval are dropped.
			metrics := batch.Metrics
			if len(metrics) > WorkerBatchRows {
				metrics = metrics[:WorkerBatchRows]
			}
			_, err := held.Call("ingest", delta.JsonObjectOf("spans", encodeSpans(batch.Spans), "metrics", encodeMetrics(metrics)), chordctx.Background())
			return true, err
		},
	}, nil
}

// Send sends one batch now, with partial the metric intervals that are not over too. It does nothing while nothing
// waits; a batch that cannot be sent now (the object is not bound) is queued again; a refused or lost one is given up.
func (sender *Sender) Send(partial bool) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	if sender.stopped || !sender.waiting(partial) {
		return
	}
	batch := sender.recorder.Take(partial, sender.limit)
	sent, err := sender.call(batch)
	_ = err // Best effort: a refused or lost batch is given up.
	sender.recorder.Release()
	if !sent {
		// Not bound now: the batch is queued again (at the back), within the recorder's bound.
		sender.recorder.Ingest(batch)
	}
}

// Stop stops the sender and releases its hold.
func (sender *Sender) Stop() {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.stopped = true
	sender.release()
}
