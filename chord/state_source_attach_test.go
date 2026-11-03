package chord

import (
	"errors"
	"slices"
	"testing"
)

// loggedSource is a numberSource that logs the members the state calls, in order, and can fail its activation.
type loggedSource struct {
	*numberSource
	log         []string
	activateErr error
}

func (source *loggedSource) Attach() (ReplicatedStateSourceAttachment[float64], error) {
	source.log = append(source.log, "Attach")
	if source.attachError != nil {
		return nil, source.attachError
	}
	return source, nil
}

func (source *loggedSource) Snapshot() ReplicatedStateSnapshot[float64] {
	source.log = append(source.log, "Snapshot")
	return source.numberSource.Snapshot()
}

func (source *loggedSource) Activate(listener func(ReplicatedStateSourceFrame[float64])) error {
	source.log = append(source.log, "Activate")
	if source.activateErr != nil {
		return source.activateErr
	}
	return source.numberSource.Activate(listener)
}

func (source *loggedSource) Dispose() {
	source.log = append(source.log, "Dispose")
	source.numberSource.Dispose()
}

// packages/chord/src/state.ts:77 (ReplicatedStateSource.attach) and :63 (ReplicatedStateSourceAttachment.activate): the state attaches once,
// reads the snapshot taken at the attach boundary, then activates the sole listener, which every later frame reaches in commit order.
func TestAttachReplicatedStateSourceAttachesThenReadsTheSnapshotThenActivatesTheSoleListener(t *testing.T) {
	source := &loggedSource{numberSource: &numberSource{value: 3, cursor: 7}}
	var contract ReplicatedStateSource[float64] = source
	state, err := AttachReplicatedStateSource(contract, ReplicatedStateSourceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Attach", "Snapshot", "Activate"}; !slices.Equal(source.log, want) {
		t.Fatalf("calls = %v, want %v", source.log, want)
	}
	if state.Value() != 3 {
		t.Fatalf("value %v, want the snapshot's 3", state.Value())
	}
	source.emit(8, 4)
	if state.Value() != 4 {
		t.Fatalf("value %v after a frame, want 4: Activate did not install the state's listener", state.Value())
	}
}

// state.ts attachReplicatedStateSource: an attachment whose activate rejects is disposed, and the activation's error is returned, no state kept.
func TestAttachReplicatedStateSourceDisposesAnAttachmentWhoseActivateFails(t *testing.T) {
	failure := errors.New("activate failed")
	source := &loggedSource{numberSource: &numberSource{value: 1, cursor: 1}, activateErr: failure}
	state, err := AttachReplicatedStateSource[float64](source, ReplicatedStateSourceOptions{})
	if !errors.Is(err, failure) || state != nil {
		t.Fatalf("got (%v, %v), want the activation error and no state", state, err)
	}
	if want := []string{"Attach", "Snapshot", "Activate", "Dispose"}; !slices.Equal(source.log, want) {
		t.Fatalf("calls = %v, want %v", source.log, want)
	}
	attachFailure := errors.New("attach failed")
	refused := &loggedSource{numberSource: &numberSource{attachError: attachFailure}}
	if _, err := AttachReplicatedStateSource[float64](refused, ReplicatedStateSourceOptions{}); !errors.Is(err, attachFailure) {
		t.Fatalf("attach error = %v", err)
	}
	if want := []string{"Attach"}; !slices.Equal(refused.log, want) {
		t.Fatalf("calls after a refused attach = %v, want only Attach: nothing to activate or dispose", refused.log)
	}
}
