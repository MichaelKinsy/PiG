package host

import (
	"context"
	"errors"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/src/next/remote/replica.ts. Replicas (§7, §22): a state member of a remote reference on the
// consumer, built from the snapshot and then changed by the provider's ops, decoded with the member's own Delta decoder
// and applied without validation or copy (`ApplyImmutable` shares structure); replaced by the snapshot of each re-hold.

// replicaKind tags a replicaState.
type replicaKind uint8

const (
	// replicaValue: a value.
	replicaValue replicaKind = iota
	// replicaFailed: a failed member (reads return the error; the provider's member failed, or a newly bound variant
	// lacks it, and either may come back).
	replicaFailed
	// replicaEnded: ended with its reference.
	replicaEnded
)

// replicaState is where a replica stands: a value, a failed member (err; last is its last good value, hasLast false
// when it had none), or ended with its reference (err).
type replicaState struct {
	kind    replicaKind
	value   delta.JsonValue
	err     error
	hasLast bool
	last    delta.JsonValue
}

// memberSnapshot is a member as a snapshot has it: a value (kind "value"), or failed (kind "failed", with err).
type memberSnapshot struct {
	kind  string
	value delta.JsonValue
	err   error
}

// replica is one state member of a hold, as the consumer reads and watches it; changed only inside the consumer's batch.
type replica struct {
	state.Source
	state replicaState
	// decoder is the member's decoder, started with the snapshot as the provider's encoder was; a new one per snapshot.
	decoder *delta.Decoder
	// ops are the ops of the current batch's frame, and retired whether the provider's document was retired in it.
	ops     []delta.Op
	retired bool
	// retiredWhileFailed is a retirement that reached it while failed: its next value carries the flag.
	retiredWhileFailed bool
}

var _ state.SourceHandle = (*replica)(nil)

// newReplica makes the replica of a member from its snapshot, on batch.
func newReplica(batch *state.Batch, snapshot memberSnapshot) *replica {
	r := &replica{decoder: delta.NewDecoder()}
	if snapshot.kind == "failed" {
		r.state = replicaState{kind: replicaFailed, err: state.HandedOut(snapshot.err)}
	} else {
		r.state = replicaState{kind: replicaValue, value: snapshot.value}
	}
	r.Init(batch, r)
	return r
}

// Value is its value; a failed or ended member returns its error.
func (r *replica) Value() (delta.JsonValue, error) {
	if r.state.kind == replicaValue {
		return r.state.value, nil
	}
	return nil, r.state.err
}

// Start is its value, or while failed its failure and last good value.
func (r *replica) Start() state.WatchStart {
	if r.state.kind == replicaFailed {
		return state.WatchStart{Kind: state.StartFailed, Error: r.state.err, HasLast: r.state.hasLast, Last: r.state.last}
	}
	value, err := r.Value()
	if err != nil {
		return state.WatchStart{Kind: state.StartFailed, Error: err}
	}
	return state.WatchStart{Kind: state.StartValue, Value: value}
}

// decode is the value wire (this member's wire ops from one state part) makes, and its ops; it changes nothing yet. It
// returns an error on ops that do not fit (a protocol error): an ended member's, and a failed member's other than its
// recovery, which the provider sends as `[["r", value]]`.
func (r *replica) decode(wire any) (delta.JsonValue, []delta.Op, error) {
	// Trust boundary: fresh JSON from the link; Delta's decoder checks each op.
	list, ok := wire.([]any)
	if !ok {
		return nil, nil, errors.New("part: a member's changes are a list of ops")
	}
	wireOps := make([]delta.WireOp, len(list))
	for index, item := range list {
		op, isOp := item.([]any)
		if !isOp {
			return nil, nil, errors.New("part: a member's changes are a list of ops")
		}
		wireOps[index] = delta.WireOp(op)
	}
	ops, err := r.decoder.Decode(wireOps)
	if err != nil {
		return nil, nil, err
	}
	switch r.state.kind {
	case replicaEnded:
		return nil, nil, r.state.err
	case replicaValue:
		value, err := delta.ApplyImmutable(r.state.value, ops)
		return value, ops, err
	}
	if len(ops) == 0 || delta.Verb(ops[0]) != "r" {
		return nil, nil, errors.New("part: a failed member recovers with a root replacement")
	}
	value, err := delta.ApplyImmutable(nil, ops)
	return value, ops, err
}

// adopt adopts the member's new value, the ops that made it and whether the provider's document was retired, in the
// current batch (one adoption per member and batch).
func (r *replica) adopt(value delta.JsonValue, ops []delta.Op, retired bool) {
	flagged := retired || r.retiredWhileFailed
	r.state = replicaState{kind: replicaValue, value: value}
	r.ops = ops
	r.retired = flagged
	r.retiredWhileFailed = false
	r.Changed()
	if flagged {
		r.RetiredInBatch()
	}
}

// retireAlone: the provider's document was retired with no ops for this member (a derive whose value stayed, rule 21),
// in the current batch: the flag alone; while failed it waits for the next value (a failed member's value is never read).
func (r *replica) retireAlone() {
	switch r.state.kind {
	case replicaValue:
		r.adopt(r.state.value, nil, true)
	case replicaFailed:
		r.retiredWhileFailed = true
	}
}

// reset takes a re-hold's snapshot of the member, in the batch that applies it, with a fresh decoder (the provider's
// encoder starts anew with it): a value is one frame `[["r", value]]`, a failed member fails.
func (r *replica) reset(snapshot memberSnapshot) {
	r.decoder = delta.NewDecoder()
	if snapshot.kind == "failed" {
		r.fail(snapshot.err)
		return
	}
	r.adopt(snapshot.value, []delta.Op{{"r", snapshot.value}}, false)
}

// fail: the provider's member failed, in the batch that applies it: reads return err, watches get one failure call and
// stay open, derives over it fail; its recovery is `[["r", value]]`.
func (r *replica) fail(err error) {
	previous := r.state
	next := replicaState{kind: replicaFailed, err: state.HandedOut(err)}
	switch previous.kind {
	case replicaValue:
		next.hasLast, next.last = true, previous.value
	case replicaFailed:
		next.hasLast, next.last = previous.hasLast, previous.last
	}
	r.state = next
	// Failed already: its watches and derives have the failure (one call per failure episode).
	if previous.kind != replicaFailed {
		r.Changed()
	}
}

// Deliver delivers this batch's frame to its watches and subscribers.
func (r *replica) Deliver(ctx context.Context) error {
	switch r.state.kind {
	case replicaFailed:
		r.FailWatches(r.state.err, ctx)
	case replicaValue:
		r.Publish(r.state.value, r.ops, r.retired, ctx)
	default:
		r.EndWatches()
	}
	r.ops = nil
	r.retired = false
	return nil
}

// vanish: the member is not in a newly bound variant, in the batch that binds it: as a failure (fail) with err; a later
// binding of a variant that has it brings it back (reset), as on the provider's host.
func (r *replica) vanish(err error) { r.fail(err) }

// end: its reference ended (outside a batch): reads return err, watches end `ended`.
func (r *replica) end(err error) {
	r.state = replicaState{kind: replicaEnded, err: err}
	r.EndWatches()
}
