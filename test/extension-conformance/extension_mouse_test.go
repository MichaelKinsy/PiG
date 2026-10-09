package extensionconformance

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// mouseProbeEvents are the fullscreen mouse events the mouse row hands the
// probe, each field distinguishable from its zero value somewhere: a hover,
// an Alt wheel up, and a click (Ctrl press, release, Shift double click).
var mouseProbeEvents = []extension.RemoteMouseEvent{
	{Type: "move", Button: "none", X: 3, Y: 1, ScreenX: 13, ScreenY: 11, Width: 30, Height: 4},
	{Type: "wheel", Button: "none", X: 3, Y: 1, ScreenX: 13, ScreenY: 11, Width: 30, Height: 4, WheelDelta: -3, Alt: true},
	{Type: "press", Button: "left", X: 7, Y: 2, ScreenX: 17, ScreenY: 12, Width: 30, Height: 4, Ctrl: true},
	{Type: "release", Button: "left", X: 7, Y: 2, ScreenX: 17, ScreenY: 12, Width: 30, Height: 4},
	{Type: "click", Button: "left", X: 7, Y: 2, ScreenX: 17, ScreenY: 12, Width: 30, Height: 4, ClickCount: 2, Shift: true},
}

// mouseProbeResult is what every SDK's probe logs for mouseProbeEvents.
const mouseProbeResult = "move/none/3,1/13,11/30x4/w0/c0/," +
	"wheel/none/3,1/13,11/30x4/w-3/c0/A," +
	"press/left/7,2/17,12/30x4/w0/c0/C," +
	"release/left/7,2/17,12/30x4/w0/c0/," +
	"click/left/7,2/17,12/30x4/w0/c2/S"

// openKitSession runs command in h and returns the session it shows, and
// the channel its handler's error arrives on.
func openKitSession(t *testing.T, h *harness, ui *kitUI, name string) (*kitSession, chan error) {
	t.Helper()
	command, ok := findCommand(h.runner, name)
	if !ok {
		t.Fatalf("%s is not registered", name)
	}
	h.ui.ClearRecorded()
	done := make(chan error, 1)
	go func() { done <- command.Handler(kitCommandContext(t, h), "") }()
	select {
	case session := <-ui.sessions:
		if session.mouse == nil {
			t.Fatalf("%s: the overlay host takes no mouse", name)
		}
		return session, done
	case err := <-done:
		t.Fatalf("%s returned before it showed its component: %v", name, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("%s never showed its component", name)
	}
	return nil, nil
}

func awaitKitResult(t *testing.T, h *harness, name string, done chan error, want string) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not close on the click", name)
	}
	if got := h.ui.Recorded(); !slices.Contains(got, want) {
		t.Fatalf("%s notified %q, want %q", name, got, want)
	}
}

// TestConformance_ExtensionMouseReachesComponent is the extension mouse row:
// in every SDK and mode, a ui.custom component that takes the
// mouse, as Pi's handleMouse does, receives each fullscreen event the host
// hands it, with every field as sent and in order, and the host reports
// each one handled. An SDK that does not mark its frames as taking the
// mouse, or drops a field, cannot produce the result.
func TestConformance_ExtensionMouseReachesComponent(t *testing.T) {
	t.Parallel()
	probeLines := []string{"mouse probe", "row 1", "row 2", "row 3"}
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			ui := newKitUI(h)
			session, done := openKitSession(t, h, ui, "mouse-probe")
			deadline := time.Now().Add(6 * time.Second)
			for !slices.Equal(session.lines(), probeLines) {
				if time.Now().After(deadline) {
					t.Fatalf("mouse-probe drew %q, want %q", session.lines(), probeLines)
				}
				time.Sleep(5 * time.Millisecond)
			}
			for _, event := range mouseProbeEvents {
				if result := session.mouse(event); !result.Handled {
					t.Fatalf("the host did not hand %s to the component: %+v", event.Type, result)
				}
			}
			awaitKitResult(t, h, "mouse-probe", done, "mouse="+mouseProbeResult+":info")
		})
	}
}

// TestConformance_ExtensionMouseClicksKitList clicks a row of kit-probe's
// select list as a terminal click arrives (press, release, click): Pi's
// SelectList selects the row on press and fires select on the click, for
// the host's list of an authoritative view and Node's own alike, and the
// callbacks reach the extension in order.
func TestConformance_ExtensionMouseClicksKitList(t *testing.T) {
	t.Parallel()
	const width = 72
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			ui := newKitUI(h)
			session, done := openKitSession(t, h, ui, "kit-probe")
			reference, _ := mustKitProbeTree(t)
			h.host.NotifyWidth(width)
			want := reference.Render(width)
			deadline := time.Now().Add(6 * time.Second)
			for !slices.Equal(session.rows(width), want) {
				if time.Now().After(deadline) {
					t.Fatalf("kit-probe drew %q, want %q", session.rows(width), want)
				}
				time.Sleep(5 * time.Millisecond)
			}
			row := slices.IndexFunc(want, func(line string) bool { return strings.Contains(line, "Track 3") })
			if row < 0 {
				t.Fatalf("kit-probe does not show Track 3: %q", want)
			}
			for _, kind := range []string{"press", "release", "click"} {
				event := extension.RemoteMouseEvent{Type: kind, Button: "left", X: 4, Y: row, ScreenX: 14, ScreenY: row + 5, Width: width, Height: len(want)}
				if kind == "click" {
					event.ClickCount = 1
				}
				// The release goes to the list that took the press, which
				// ignores it, as Pi's does; only Node's component, which takes
				// the mouse itself, reports it handled.
				if result := session.mouse(event); !result.Handled && kind != "release" {
					t.Fatalf("%s on Track 3 (row %d) was not handled: %+v", kind, row, result)
				}
			}
			awaitKitResult(t, h, "kit-probe", done, "kit=selectionChange:3:k3,select:3:k3:info")
		})
	}
}
