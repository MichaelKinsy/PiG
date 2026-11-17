package subprocess

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
)

// NotifyReportError (Pi 18e99051be24, runtime.reportError): the host reports what a connection sends as one of that extension's errors, and drops a report
// from a generation a reload replaced and a malformed one without ending the connection.

type reportErrorRig struct {
	h    *Host
	me   *managedExt
	mu   sync.Mutex
	got  []extension.ExtensionError
	done <-chan struct{}
	peer net.Conn
}

func newReportErrorRig(t *testing.T) (*reportErrorRig, func(method string, args any)) {
	t.Helper()
	h, me, peer, handled := supersedeHost(t, newManualLivenessClock())
	rig := &reportErrorRig{h: h, me: me, done: handled, peer: peer}
	runner := inproc.NewRunner(nil, t.TempDir(), h.Runtime())
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		rig.mu.Lock()
		rig.got = append(rig.got, *err)
		rig.mu.Unlock()
	})
	send := func(method string, args any) {
		t.Helper()
		var raw json.RawMessage
		switch v := args.(type) {
		case string:
			raw = json.RawMessage(v)
		default:
			var err error
			if raw, err = json.Marshal(v); err != nil {
				t.Fatal(err)
			}
		}
		writeLivenessEnvelope(t, peer, Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: method, Args: raw}})
	}
	return rig, send
}

// settled reports what the runner received once the host has read every frame written so far: a probe report the host answers in order marks the end.
func (r *reportErrorRig) settled(t *testing.T, send func(string, any), marker string) []extension.ExtensionError {
	t.Helper()
	send(NotifyReportError, ReportErrorPayload{Event: "events:probe", Error: marker})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := append([]extension.ExtensionError(nil), r.got...)
		r.mu.Unlock()
		if n := len(got); n > 0 && got[n-1].Error == marker {
			return got[:n-1]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the host never read the probe report %q", marker)
	return nil
}

// A report is one error of the sending extension: its path from the connection, the event and message it sent and its stack, in the order the frames were written.
func TestReportErrorNotifyIsReportedAsTheExtensionsErrorInOrder(t *testing.T) {
	rig, send := newReportErrorRig(t)
	send(NotifyReportError, ReportErrorPayload{Event: "events:ch", Error: "one", Stack: "one\n    at a"})
	send(NotifyReportError, ReportErrorPayload{Event: "events:ch", Error: "two"})
	got := rig.settled(t, send, "probe")
	path, _ := extensionSourcePaths(rig.me.config)
	if len(got) != 2 || got[0].Error != "one" || got[0].Stack != "one\n    at a" || got[1].Error != "two" || got[1].Stack != "" {
		t.Fatalf("reported %+v, want one then two with their stacks", got)
	}
	for _, e := range got {
		if e.ExtensionPath != path || path == "" || e.Event != "events:ch" {
			t.Errorf("error %+v, want path %q and event events:ch", e, path)
		}
	}
}

// A report from a generation a reload replaced is dropped: the replacement's runner reports for the extension now. (The read loop of a replaced generation
// does not serve its notifications either, so the host's check is exercised directly.)
// mutation-checked: removing the replaced check of reportExtensionError reports it.
func TestReportErrorNotifyFromAReplacedGenerationIsDropped(t *testing.T) {
	rig, _ := newReportErrorRig(t)
	rig.me.replaced.Store(true)
	rig.h.reportExtensionError(rig.me, "events:ch", "stale generation", "")
	rig.mu.Lock()
	got := append([]extension.ExtensionError(nil), rig.got...)
	rig.mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("reported %+v for a replaced generation, want none", got)
	}
	rig.me.replaced.Store(false)
	rig.h.reportExtensionError(rig.me, "events:ch", "live generation", "")
	rig.mu.Lock()
	defer rig.mu.Unlock()
	if len(rig.got) != 1 || rig.got[0].Error != "live generation" {
		t.Fatalf("reported %+v for the live generation, want the one", rig.got)
	}
}

// A malformed report (a payload that is not an object, a missing or empty event) is dropped without ending the connection: the next report is read.
func TestMalformedReportErrorNotifyIsDroppedAndTheConnectionGoesOn(t *testing.T) {
	rig, send := newReportErrorRig(t)
	send(NotifyReportError, `"just a string"`)
	send(NotifyReportError, `{"error":"no event"}`)
	send(NotifyReportError, `{"event":"","error":"empty event"}`)
	send(NotifyReportError, `{"event":7,"error":"event of the wrong type"}`)
	send(NotifyReportError, ReportErrorPayload{Event: "events:ch", Error: strings.Repeat("x", 1<<20)})
	got := rig.settled(t, send, "probe")
	if len(got) != 1 || got[0].Error != strings.Repeat("x", 1<<20) {
		t.Fatalf("reported %d errors, want only the well-formed one (a 1 MiB message is within the frame cap)", len(got))
	}
}

// Once the connection's extension is gone its read loop has ended, so nothing it could still send is read: no report arrives after the end.
func TestReportErrorNotifyAfterTheConnectionEndedReportsNothing(t *testing.T) {
	rig, send := newReportErrorRig(t)
	send(NotifyReportError, ReportErrorPayload{Event: "events:ch", Error: "before the end"})
	if got := rig.settled(t, send, "probe"); len(got) != 1 {
		t.Fatalf("reported %+v before the end, want the one", got)
	}
	_ = rig.peer.Close()
	select {
	case <-rig.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the read loop did not end with its connection")
	}
	rig.mu.Lock()
	n := len(rig.got)
	rig.mu.Unlock()
	if n != 2 {
		t.Fatalf("%d errors after the end, want the report and the probe only", n)
	}
}
