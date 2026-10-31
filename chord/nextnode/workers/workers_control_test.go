package workers

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next-node/workers/control.test.ts: the control channel's crossings: errors cross as
// `{ message, code?, reason? }` and back, and a control message is told from anything else on the channel.

func TestErrorsCrossAsMessageCodeAndReasonNeverAStackOrCauseComeBackAsChordErrorsWhenTheyHadACode(t *testing.T) {
	chord := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "gone"}, errors.New("inner"))
	if got := crossed(chord); got != (CrossedError{Message: "gone", Code: "unavailable"}) {
		t.Fatalf("crossed(chord) = %+v", got)
	}
	if got := crossed(errors.New("plain")); got != (CrossedError{Message: "plain"}) {
		t.Fatalf("crossed(plain) = %+v", got)
	}
	if got := crossed("text"); got != (CrossedError{Message: "text"}) {
		t.Fatalf("crossed(text) = %+v", got)
	}
	back := uncrossed(CrossedError{Message: "gone", Code: "released", Reason: "closed"})
	released, ok := data.IsChordErrorCode(back, data.CodeReleased)
	if !ok || released.Reason != data.ReasonClosed {
		t.Fatalf("uncrossed = %#v, want a released/closed ChordError", back)
	}
	odd := uncrossed(CrossedError{Message: "odd", Code: "no-such-code"})
	if _, isChord := data.IsChordError(odd); isChord || odd.Error() != "odd" {
		t.Fatalf("uncrossed(unknown code) = %#v, want a plain error with the message", odd)
	}
}

func TestEnvelopesTellControlMessagesFromAnythingElseOnTheChannel(t *testing.T) {
	payload, err := envelope(FromWorker{Kind: FromWorkerStarted})
	if err != nil {
		t.Fatal(err)
	}
	got, ok := controlOf[FromWorker](json.RawMessage(payload))
	if !ok || got.Kind != FromWorkerStarted {
		t.Fatalf("controlOf(envelope) = %+v, %v", got, ok)
	}
	if _, ok := controlOf[FromWorker]([]byte{0}); ok {
		t.Fatal("a link frame is a control message")
	}
	if _, ok := controlOf[FromWorker](json.RawMessage(`{"kind":"started"}`)); ok {
		t.Fatal("an object without the envelope is a control message")
	}
}
