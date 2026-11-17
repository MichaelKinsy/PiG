package subprocess

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The in-heap pi.events bus of a shared Node realm runs the listener of one extension inside the emit of another, so Pi's synchronous reportError of that
// listener is ordered before the emitter's response only on the emitter's connection: the runtime sends the report there and names the extension whose
// listener failed (ReportErrorPayload.Extension). The host takes the name for that extension's own error when it runs in the connection's process, and
// drops a name it does not know or one that runs elsewhere, as it drops any malformed report.

// mutation-checked: a host that reports every report as the connection's own extension's (ignoring ReportErrorPayload.Extension) reports all four names under the connection's path.
func TestReportErrorNotifyNamingAnotherExtensionIsThatExtensionsErrorInItsProcessOnly(t *testing.T) {
	rig, send := newReportErrorRig(t)
	sibling := &managedExt{config: ExtConfig{Name: "sibling", Path: "/sibling.mjs"}, host: rig.h, procOwner: rig.me}
	foreign := &managedExt{config: ExtConfig{Name: "foreign", Path: "/foreign.mjs"}, host: rig.h}
	rig.h.mu.Lock()
	rig.h.exts["sibling"] = sibling
	rig.h.exts["foreign"] = foreign
	rig.h.mu.Unlock()
	send(NotifyReportError, `{"event":"events:ch","error":"by the sibling","extension":"sibling"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by another process","extension":"foreign"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by nobody","extension":"nobody"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by itself","extension":"old"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by the connection"}`)
	got := rig.settled(t, send, "probe")
	own, _ := extensionSourcePaths(rig.me.config)
	siblingPath, _ := extensionSourcePaths(sibling.config)
	want := []extension.ExtensionError{
		{ExtensionPath: siblingPath, Event: "events:ch", Error: "by the sibling"},
		{ExtensionPath: own, Event: "events:ch", Error: "by itself"},
		{ExtensionPath: own, Event: "events:ch", Error: "by the connection"},
	}
	if len(got) != len(want) {
		t.Fatalf("reported %+v, want %+v (a name of another process or of no extension is dropped)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("report %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
