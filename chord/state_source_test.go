package chord

// pi: packages/chord/src/state.ts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// numberSource is an authoritative source the test drives frame by frame.
type numberSource struct {
	value       float64
	cursor      int
	listener    func(ReplicatedStateSourceFrame[float64])
	disposed    int
	attachError error
}

func (source *numberSource) Attach() (ReplicatedStateSourceAttachment[float64], error) {
	return source, source.attachError
}
func (source *numberSource) Snapshot() ReplicatedStateSnapshot[float64] {
	return ReplicatedStateSnapshot[float64]{Value: source.value, Cursor: source.cursor}
}
func (source *numberSource) Activate(listener func(ReplicatedStateSourceFrame[float64])) error {
	source.listener = listener
	return nil
}
func (source *numberSource) Dispose() { source.disposed++ }
func (source *numberSource) emit(cursor int, value float64) {
	source.listener(ReplicatedStateSourceFrame[float64]{Cursor: cursor, Value: value, Ops: []delta.Op{{"r", value}}, Context: context.Background()})
}

// upstream: state.ts attachReplicatedStateSource: the attachment's snapshot is the first value (a hydration), each next-cursor frame is an update, and a cursor gap disposes the attachment and reports the failure.
// Pi source: packages/chord/src/state.ts (attachReplicatedStateSource)
// Pi: packages/chord/src/state.ts:77 (ReplicatedStateSource.attach), :57 (the attachment snapshot), :63 (activate), :65 (dispose)
// mutation-checked: a wrong expected cursor, or not disposing on a gap, each fail it
// mutation-checked: dropping the reads and writes of ReplicatedStateDelivery.Kind, ReplicatedStateSourceOptions.OnError fails it
// mutation-checked: zeroing the results of AttachedReplicatedState.Value fails it
func TestAttachReplicatedStateSourceDeliversSnapshotThenFramesAndRejectsAGap(t *testing.T) {
	source := &numberSource{value: 1, cursor: 4}
	var reported []error
	state, err := AttachReplicatedStateSource[float64](source, ReplicatedStateSourceOptions{OnError: func(err error) { reported = append(reported, err) }})
	if err != nil {
		t.Fatal(err)
	}
	var kinds []DeliveryKind
	var values []float64
	state.Subscribe(func(value float64, _ context.Context, delivery ReplicatedStateDelivery) {
		kinds, values = append(kinds, delivery.Kind), append(values, value)
	})
	source.emit(5, 2)
	source.emit(6, 3)
	if state.Value() != 3 || len(kinds) != 3 || kinds[0] != DeliveryHydrate || kinds[1] != DeliveryUpdate || kinds[2] != DeliveryUpdate || values[0] != 1 || values[2] != 3 {
		t.Fatalf("value %v, deliveries %v %v", state.Value(), kinds, values)
	}
	source.emit(8, 9)
	if state.Value() != 3 || source.disposed != 1 || len(reported) != 1 || !strings.Contains(reported[0].Error(), "expected 7, received 8") {
		t.Fatalf("after a gap: value %v, disposed %d, reported %v", state.Value(), source.disposed, reported)
	}
	source.emit(7, 7)
	if state.Value() != 3 {
		t.Fatal("a frame after the failed attachment was delivered")
	}
}

// upstream: state.ts attachReplicatedStateSource: a source that fails to attach fails the attachment without a state.
// Pi source: packages/chord/src/state.ts (attachReplicatedStateSource)
// mutation-checked: swallowing the attach error fails it
func TestAttachReplicatedStateSourceReturnsTheAttachError(t *testing.T) {
	failure := errors.New("attach failed")
	state, err := AttachReplicatedStateSource[float64](&numberSource{attachError: failure}, ReplicatedStateSourceOptions{})
	if !errors.Is(err, failure) || state != nil {
		t.Fatalf("got (%v, %v), want the attach error and no state", state, err)
	}
}

// TestConformanceReplicatedStateSourceContract drives a source through the interface members Pi's chord/src/state.ts declares:
// packages/chord/src/state.ts:77 ReplicatedStateSource.attach returns an attachment that captured its snapshot at the attach
// boundary (:57), packages/chord/src/state.ts:63 activate installs the sole listener and every later committed frame reaches it in
// order, and packages/chord/src/state.ts:65 dispose is idempotent and stops delivery.
func TestConformanceReplicatedStateSourceContract(t *testing.T) {
	source := &numberSource{value: 3, cursor: 7}
	var contract ReplicatedStateSource[float64] = source
	attachment, err := contract.Attach()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot := attachment.Snapshot(); snapshot.Value != 3 || snapshot.Cursor != 7 {
		t.Fatalf("snapshot %+v, want value 3 at cursor 7", snapshot)
	}
	var seen []float64
	if err := attachment.Activate(func(frame ReplicatedStateSourceFrame[float64]) { seen = append(seen, frame.Value) }); err != nil {
		t.Fatal(err)
	}
	source.emit(8, 4)
	source.emit(9, 5)
	attachment.Dispose()
	attachment.Dispose()
	if len(seen) != 2 || seen[0] != 4 || seen[1] != 5 {
		t.Fatalf("frames %v, want [4 5]", seen)
	}
	if source.disposed != 2 {
		t.Fatalf("disposed %d times, want the two calls to reach the attachment", source.disposed)
	}
}
