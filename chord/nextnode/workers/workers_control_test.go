package workers

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// pi: packages/chord/src/next-node/workers/control.ts
// Ports packages/chord/test/node/workers/control.test.ts: the control channel's crossings: errors cross as
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

func TestErrorsCrossAReportsFailuresAndParkedCausesAndBack(t *testing.T) {
	report := crossedReport(host.ReloadReport{
		Plugins: host.PluginChanges{Added: []string{"@a/x"}, Changed: []string{}, Removed: []string{}},
		Failed:  []host.PluginFailure{{Plugin: "@a/y", Error: errors.New("broken")}},
		Objects: []host.ReloadedObject{
			{Object: "a.one", Kind: host.ReloadRestarted},
			{
				Object: "a.two", Kind: host.ReloadParked,
				Cause: data.FailureCause{Kind: data.FailureWaiting, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: "w"}, nil)},
			},
		},
	})
	// It is plain JSON: it comes back from its text as it was.
	text, err := json.Marshal(report.json())
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := delta.DecodeJson(text)
	if err != nil {
		t.Fatal(err)
	}
	again, err := crossedReportFromJSON(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*again, report) {
		t.Fatalf("report after its JSON = %+v, want %+v", *again, report)
	}
	back := uncrossedReport(report)
	if back.Failed[0].Error == nil || back.Failed[0].Error.Error() != "broken" {
		t.Fatalf("failed[0].error = %v", back.Failed[0].Error)
	}
	parked := back.Objects[1]
	if parked.Kind != host.ReloadParked || parked.Cause.Kind != data.FailureWaiting || parked.Cause.Error == nil || parked.Cause.Error.Code != data.CodeUnavailable {
		t.Fatalf("objects[1] = %+v", parked)
	}
	// A parked object's cause is a ChordError on the worker; one that crossed without a code is wrapped as one.
	plain := uncrossedReport(CrossedReport{Objects: []CrossedObject{{Object: "a.three", Kind: "parked", Cause: &CrossedCause{Kind: "waiting", Error: CrossedError{Message: "plain"}}}}})
	if cause := plain.Objects[0].Cause; cause.Error == nil || cause.Error.Code != data.CodeUnavailable || cause.Error.Message != "plain" {
		t.Fatalf("wrapped cause = %+v", cause.Error)
	}
}
