package subprocess

import (
	"encoding/json"
	"io"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// pig additive (D107): a rejected extension view is reported through the runner's error listeners in every runner state, not Pi's runtime.reportError (Pi
// 18e99051be24), whose stale guard belongs to the pi.events path alone. The runner's constructor binds Pi's action; the D107 reporter stays bound in BindCore,
// so a view rejected between the two writes where it wrote before, and a runner a failed reload left stale, whose old runtime still draws, still reports.
//
// rejectedViewReports renders one rejected view on a host whose runner went through prepare and returns what the runner's listeners received and what the
// extension was sent.
func rejectedViewReports(t *testing.T, prepare func(*inproc.Runner)) (reported []*extension.ExtensionError, sent int64) {
	t.Helper()
	withoutViewWidgetBuilders(t)
	h := NewHost(t.TempDir())
	conn, peer := newPipeConn()
	done := make(chan int64, 1)
	go func() {
		n, _ := io.Copy(io.Discard, peer)
		done <- n
	}()
	h.observeXref(conn)
	h.mu.Lock()
	h.exts["peer"] = &managedExt{config: ExtConfig{Name: "peer", Source: "/ext/peer.go"}}
	h.mu.Unlock()
	runner := inproc.NewRunner(nil, t.TempDir(), h.Runtime())
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
	prepare(runner)
	var view rendererView
	if lines, ok := view.apply(conn, func() {}, "", nil, RenderResult{View: json.RawMessage(gridOnlyView)}, 80); ok || lines != nil {
		t.Fatalf("a view of a kind that is not enabled rendered %q, ok %v: want a failed render", lines, ok)
	}
	_ = peer.Close()
	return reported, <-done
}

// A rejected view on a runner that is stale after a failed reload (InvalidateForReload: the old runtime still draws) is reported once, as before: the stale
// guard of the events reporter does not reach it.
// mutation-checked: moving the D107 reporter behind the runner's stale check silences it.
func TestRejectedViewOnARunnerStaleAfterAFailedReloadIsStillReportedOnce(t *testing.T) {
	reported, sent := rejectedViewReports(t, func(r *inproc.Runner) {
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
		r.InvalidateForReload("")
	})
	if len(reported) != 1 || reported[0].Event != "view" || reported[0].ExtensionPath != "/ext/peer.go" {
		t.Fatalf("reported %+v on a stale runner, want the rejected view once", reported)
	}
	if sent != 0 {
		t.Fatalf("the extension was sent %d bytes for the rejected view, want nothing", sent)
	}
}

// The same on a retired runner (Invalidate, as a session replacement does): D107's report reaches the retired runner's own listeners as it did before the
// events reporter existed.
func TestRejectedViewOnARetiredRunnerIsReportedToItsOwnListenersAsBefore(t *testing.T) {
	reported, _ := rejectedViewReports(t, func(r *inproc.Runner) {
		r.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
		r.Invalidate("")
	})
	if len(reported) != 1 || reported[0].Event != "view" {
		t.Fatalf("reported %+v on a retired runner, want the rejected view once", reported)
	}
}

// Between the runner's construction and BindCore no D107 reporter is bound: the rejection is not delivered to the runner's listeners (the host writes it on
// stderr outside the TUI), exactly as before the events reporter moved into the constructor.
// mutation-checked: binding the D107 reporter in the constructor delivers it here.
func TestRejectedViewBetweenTheRunnerConstructionAndBindCoreIsNotDelivered(t *testing.T) {
	reported, _ := rejectedViewReports(t, func(*inproc.Runner) {})
	if len(reported) != 0 {
		t.Fatalf("reported %+v before BindCore, want none", reported)
	}
}
