package host

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/state"
)

// Ports packages/chord/test/next/remote/replica.test.ts: replicas.

// replicaOfN is a replica of `{ n }` on a fresh batch, with a derive over it attached.
func replicaOfN(t *testing.T) (*state.Batch, *replica, *state.Derive) {
	t.Helper()
	batch := state.NewBatch(func(state.BatchError) {}, nil)
	source := newReplica(batch, memberSnapshot{kind: "value", value: delta.JsonObjectOf("n", 1.0)})
	double := state.NewDerive([]any{source}, func(values []any) (delta.JsonValue, error) {
		return values[0].(*delta.JsonObject).Value("n").(float64) * 2, nil
	}, batch)
	if err := double.Attach(func(declared any, _ int) (state.DeriveInput, error) { return declared.(*replica), nil }, func(error) {}); err != nil {
		t.Fatal(err)
	}
	return batch, source, double
}

func replicaCommit(t *testing.T, batch *state.Batch, adopt func()) {
	t.Helper()
	if err := batch.Commit(func() error { adopt(); return nil }, background); err != nil {
		t.Fatal(err)
	}
}

func replicaWatch(t *testing.T, source interface {
	OpenWatch(context.Context) (*chord.Watch[delta.JsonValue], error)
}) *chord.Watch[delta.JsonValue] {
	t.Helper()
	watch, err := source.OpenWatch(background)
	if err != nil {
		t.Fatal(err)
	}
	return watch
}

func TestReplicasAdoptAValueAndItsOpsInsideABatchOneFrameDerivesRecomputed(t *testing.T) {
	batch, source, double := replicaOfN(t)
	frames := nexttest.FramesOf(replicaWatch(t, source))
	doubles := nexttest.FramesOf(replicaWatch(t, double))
	ops := []delta.Op{{"s", []any{"n"}, 2.0}}
	replicaCommit(t, batch, func() { source.adopt(delta.JsonObjectOf("n", 2.0), ops, false) })
	nexttest.Delivered()
	got := frames.Items()
	if len(got) != 1 {
		t.Fatalf("frames = %d, want 1", len(got))
	}
	nexttest.RequireJSONEqual(t, got[0].Value, delta.JsonObjectOf("n", 2.0))
	nexttest.RequireJSONEqual(t, jsonOps(got[0].Ops), []any{[]any{"s", []any{"n"}, 2.0}})
	gotDoubles := doubles.Items()
	if len(gotDoubles) != 1 {
		t.Fatalf("derive frames = %d, want 1", len(gotDoubles))
	}
	nexttest.RequireJSONEqual(t, gotDoubles[0].Value, 4.0)
	nexttest.RequireJSONEqual(t, jsonOps(gotDoubles[0].Ops), []any{[]any{"r", 4.0}})
}

func TestReplicasFailReadsReturnTheErrorWatchesAndDerivesOverItGetOneFailureCallAndRecoverWithARootReplacement(t *testing.T) {
	batch, source, double := replicaOfN(t)
	calls := nexttest.ChangesOf(replicaWatch(t, source))
	derived := nexttest.ChangesOf(replicaWatch(t, double))
	failure := errors.New("member failed")
	replicaCommit(t, batch, func() { source.fail(failure) })
	nexttest.Delivered()
	for name, got := range map[string][]chord.WatchCall[delta.JsonValue]{"source": calls.Items(), "derive": derived.Items()} {
		if len(got) != 1 || got[0].Kind != chord.WatchCallFailed || !errors.Is(got[0].Error, failure) {
			t.Fatalf("%s calls = %+v, want one failure with the error", name, got)
		}
	}
	if _, err := source.Value(); !errors.Is(err, failure) {
		t.Fatalf("source.Value error = %v, want %v", err, failure)
	}
	// A non-root op cannot apply to a failed member; its recovery is a root replacement.
	if _, _, err := source.decode([]any{[]any{"s", []any{"n"}, 2.0}}); err == nil || err.Error() != "part: a failed member recovers with a root replacement" {
		t.Fatalf("decode of a non-root op error = %v, want the root replacement refusal", err)
	}
	value, ops, err := source.decode([]any{[]any{"r", delta.JsonObjectOf("n", 3.0)}})
	if err != nil {
		t.Fatal(err)
	}
	replicaCommit(t, batch, func() { source.adopt(value, ops, false) })
	nexttest.Delivered()
	last := calls.Items()[calls.Len()-1]
	if last.Kind != chord.WatchCallValue || last.Retired {
		t.Fatalf("last source call = %+v, want a value call, not retired", last)
	}
	nexttest.RequireJSONEqual(t, last.Value, delta.JsonObjectOf("n", 3.0))
	nexttest.RequireJSONEqual(t, jsonOps(last.Ops), []any{[]any{"r", delta.JsonObjectOf("n", 3.0)}})
	lastDerived := derived.Items()[derived.Len()-1]
	if lastDerived.Kind != chord.WatchCallValue || lastDerived.Retired {
		t.Fatalf("last derive call = %+v, want a value call, not retired", lastDerived)
	}
	nexttest.RequireJSONEqual(t, lastDerived.Value, 6.0)
	nexttest.RequireJSONEqual(t, jsonOps(lastDerived.Ops), []any{[]any{"r", 6.0}})
}

func TestReplicasFailOncePerEpisodeASecondFailureWhileFailedGivesNoCallARetirementMeanwhileWaitsForTheValue(t *testing.T) {
	batch, source, _ := replicaOfN(t)
	calls := nexttest.ChangesOf(replicaWatch(t, source))
	first := errors.New("first")
	replicaCommit(t, batch, func() { source.fail(first) })
	replicaCommit(t, batch, func() { source.fail(errors.New("second")) })
	nexttest.Delivered()
	// A retirement alone while failed: the value is not read, the flag waits.
	replicaCommit(t, batch, func() { source.retireAlone() })
	value, ops, err := source.decode([]any{[]any{"r", delta.JsonObjectOf("n", 5.0)}})
	if err != nil {
		t.Fatal(err)
	}
	replicaCommit(t, batch, func() { source.adopt(value, ops, false) })
	nexttest.Delivered()
	got := calls.Items()
	if len(got) != 2 {
		t.Fatalf("calls = %+v, want a failure and a value", got)
	}
	if got[0].Kind != chord.WatchCallFailed || !errors.Is(got[0].Error, first) {
		t.Fatalf("first call = %+v, want the first failure", got[0])
	}
	if got[1].Kind != chord.WatchCallValue || !got[1].Retired {
		t.Fatalf("second call = %+v, want a retired value", got[1])
	}
	nexttest.RequireJSONEqual(t, got[1].Value, delta.JsonObjectOf("n", 5.0))
	nexttest.RequireJSONEqual(t, jsonOps(got[1].Ops), []any{[]any{"r", delta.JsonObjectOf("n", 5.0)}})
}

func TestReplicasOpenANewWatchWhileFailedAtTheLastValueTheFailureFirstWithNoValueYetRejectIt(t *testing.T) {
	batch, source, _ := replicaOfN(t)
	failure := errors.New("member failed")
	replicaCommit(t, batch, func() { source.fail(failure) })
	watch := replicaWatch(t, source)
	nexttest.RequireJSONEqual(t, watch.Value(), delta.JsonObjectOf("n", 1.0))
	calls := nexttest.ChangesOf(watch)
	nexttest.Delivered()
	got := calls.Items()
	if len(got) != 1 || got[0].Kind != chord.WatchCallFailed || !errors.Is(got[0].Error, failure) {
		t.Fatalf("calls = %+v, want one failure", got)
	}
	// Failed since its binding: there is no value to start at.
	unborn := newReplica(batch, memberSnapshot{kind: "failed", err: failure})
	if _, err := unborn.OpenWatch(background); !errors.Is(err, failure) {
		t.Fatalf("OpenWatch error = %v, want %v", err, failure)
	}
}

func TestReplicasEndWithTheirHoldReadsReturnItsErrorWatchesEnd(t *testing.T) {
	_, source, _ := replicaOfN(t)
	watch := replicaWatch(t, source)
	ended := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "app.notes: ended"}, nil)
	source.end(ended)
	<-watch.Closed()
	if end := watch.End(); end.Kind != chord.WatchEnded {
		t.Fatalf("end = %+v, want ended", end)
	}
	if _, err := source.Value(); err == nil || err.Error() != "app.notes: ended" {
		t.Fatalf("Value error = %v, want app.notes: ended", err)
	}
}

// jsonOps is ops as plain JSON arrays, as the comparisons read them.
func jsonOps(ops []delta.Op) []any {
	out := make([]any, len(ops))
	for index, op := range ops {
		out[index] = []any(op)
	}
	return out
}
