package subprocess

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// The in-heap pi.events bus of a shared Node realm runs the listener of one extension inside the emit of another, so Pi's synchronous reportError of that
// listener is ordered before the emitter's response only on the emitter's connection: the runtime sends the report there and names the extension whose
// listener failed (ReportErrorPayload.Extension). That is not multi-registration: the host takes the name only for an extension of the connection's own
// process (the shared realm); any other name is ignored, the report is applied as the connection's own extension's, in its place in the order, and the
// refused name is reported first as an error of the connection's extension through the runner's error listeners.

// reportErrorSiblingRig is a report_error rig whose host also knows a sibling of the connection's own process and an extension of another process.
func reportErrorSiblingRig(t *testing.T) (*reportErrorRig, func(method string, args any), *managedExt) {
	t.Helper()
	rig, send := newReportErrorRig(t)
	sibling := &managedExt{config: ExtConfig{Name: "sibling", Path: "/sibling.mjs"}, host: rig.h, procOwner: rig.me}
	foreign := &managedExt{config: ExtConfig{Name: "foreign", Path: "/foreign.mjs"}, host: rig.h}
	rig.h.mu.Lock()
	rig.h.exts["sibling"] = sibling
	rig.h.exts["foreign"] = foreign
	rig.h.mu.Unlock()
	return rig, send, sibling
}

// mutation-checked: a host that reports every report as the connection's own extension's (ignoring ReportErrorPayload.Extension) reports the sibling's under the connection's path.
func TestReportErrorNotifyNamingAnotherExtensionIsThatExtensionsErrorInItsProcessOnly(t *testing.T) {
	rig, send, sibling := reportErrorSiblingRig(t)
	send(NotifyReportError, `{"event":"events:ch","error":"by the sibling","extension":"sibling"}`)
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
		t.Fatalf("reported %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("report %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A tag that does not name an extension of the connection's own process is never attributed to the extension it names: the report is the connection's own extension's (a process that sent the report did send it, and an error report is not lost), in order, and the refused name is reported first as an error of the connection's extension through the runner's error listeners, the mode-aware path of every extension error: nothing is written to the terminal's own stderr (in the interactive mode a raw line would draw over the screen).
//
// mutation-checked: a host that takes any tag (no check that the named extension runs in the connection's process) attributes the foreign extension's name to /foreign.mjs; a host that drops the report loses "by another process" and "by nobody".
func TestReportErrorNotifyNamingAnExtensionOutsideTheConnectionsProcessIsTheConnectionsOwnAndTheNameIsRefused(t *testing.T) {
	collect := swapStd(t)
	rig, send, _ := reportErrorSiblingRig(t)
	send(NotifyReportError, `{"event":"events:ch","error":"first"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by another process","extension":"foreign"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"by nobody","extension":"nobody"}`)
	send(NotifyReportError, `{"event":"events:ch","error":"last"}`)
	got := rig.settled(t, send, "probe")
	_, stderr := collect()
	own, _ := extensionSourcePaths(rig.me.config)
	refusal := func(name string) extension.ExtensionError {
		return extension.ExtensionError{ExtensionPath: own, Event: "events:ch", Error: fmt.Sprintf("report_error names the extension %q, which does not run in the process of old: reported as old's error", name)}
	}
	want := []extension.ExtensionError{
		{ExtensionPath: own, Event: "events:ch", Error: "first"},
		refusal("foreign"),
		{ExtensionPath: own, Event: "events:ch", Error: "by another process"},
		refusal("nobody"),
		{ExtensionPath: own, Event: "events:ch", Error: "by nobody"},
		{ExtensionPath: own, Event: "events:ch", Error: "last"},
	}
	if len(got) != len(want) {
		t.Fatalf("reported %+v, want %+v (a refused name is reported as an error of the connection's extension, then the report is the connection's own, in order)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("report %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if stderr != "" {
		t.Errorf("the host wrote %q to the terminal's stderr, want nothing: the refused name goes through the runner's error listeners", stderr)
	}
}
