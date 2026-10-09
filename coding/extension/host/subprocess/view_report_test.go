package subprocess

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// A rejected view is reported once per surface, however often the surface
// rejects a frame, and each surface reports its own (§5.2, §13.4).
func TestRejectedViewsAreReportedOncePerSurface(t *testing.T) {
	var reports []string
	report := func(surface string, err error) { reports = append(reports, surface) }
	first, second := newViewSurface("", newViewImageStore()), newViewSurface("", newViewImageStore())
	first.report, second.report = report, report
	invalid := json.RawMessage(`{"root":{"kind":"widget"}}`)
	for range 3 {
		if acceptView(first, "widget a", invalid, nil) {
			t.Fatal("an invalid view was accepted")
		}
	}
	if !acceptView(first, "widget a", json.RawMessage(`{"root":{"kind":"spacer"}}`), nil) {
		t.Fatal("a valid view was rejected")
	}
	acceptView(first, "widget a", invalid, nil)
	acceptView(second, "widget b", invalid, nil)
	if got := strings.Join(reports, ","); got != "widget a,widget b" {
		t.Fatalf("reports = %q, want one per surface", got)
	}
}

// The report reaches the runner's error listeners, the channel each mode
// shows extension errors on, as an ExtensionError of the extension's path.
// Without a runner it goes to stderr, except in the TUI, which owns the
// terminal.
func TestRejectedViewsReachTheRunnersErrorListeners(t *testing.T) {
	var out bytes.Buffer
	previous := viewDiagnosticOut
	viewDiagnosticOut = &out
	t.Cleanup(func() { viewDiagnosticOut = previous })

	h := NewHost(t.TempDir())
	conn := &Conn{name: "music"}
	h.observeXref(conn)
	h.mu.Lock()
	h.exts["music"] = &managedExt{config: ExtConfig{Name: "music", Source: "/ext/music.go"}}
	h.mu.Unlock()
	rejection := errors.New(`unknown view kind "widget"`)

	for _, mode := range []string{"tui", "print"} {
		out.Reset()
		h.SetMode(mode)
		conn.reportView("widget play", rejection)
		if got := out.String(); (mode == "tui") != (got == "") || (mode != "tui" && !strings.Contains(got, `extension view rejected on widget play`)) {
			t.Fatalf("%s without a runner wrote %q", mode, got)
		}
	}

	var reported []*extension.ExtensionError
	runner := inproc.NewRunner(nil, t.TempDir(), h.Runtime())
	runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	out.Reset()
	conn.reportView("widget play", rejection)
	if out.Len() != 0 || len(reported) != 1 || reported[0].ExtensionPath != "/ext/music.go" || reported[0].Event != "view" ||
		!strings.Contains(reported[0].Error, `widget play`) || !strings.Contains(reported[0].Error, `unknown view kind "widget"`) {
		t.Fatalf("reported = %+v, stderr %q", reported, out.String())
	}
}
