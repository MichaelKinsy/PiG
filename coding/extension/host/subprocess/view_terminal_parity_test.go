package subprocess

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/tui"
)

// Exact-ANSI parity of the component kit (D107, spec §1 and §5): a view
// never changes what the terminal shows. The same widget frames reach three
// terminals: as lines only (every extension before the kit), as the same
// lines annotated with the view that describes them (Node while a frontend
// draws), and as the authoritative view alone (Go, Rust and Python). Every
// frame writes the same bytes to all three.
func TestViewsLeaveTheTerminalBytesUnchanged(t *testing.T) {
	const width = 72
	type terminal struct {
		name   string
		bridge *UIBridge
		ui     *tui.TuiMainScreen
		out    *bytes.Buffer
	}
	var terminals []*terminal
	for _, name := range []string{"lines", "annotated", "authoritative"} {
		var out bytes.Buffer
		ui := tui.NewWithOutput(&out, width, 40)
		ui.SetLogDirectory(t.TempDir())
		bridge := newTestBridge(&mockUIContext{})
		bridge.SetFrontend(name == "annotated")
		terminals = append(terminals, &terminal{name, bridge, ui, &out})
	}
	// The lines every terminal draws come from the tui ports directly, as an
	// in-process Pi extension draws them.
	direct, list := directProbe(t)
	for frame, selected := range []int{2, 3, 4, 0} {
		view := strings.Replace(kitProbeView, `"selectedIndex":2`, `"selectedIndex":`+strconv.Itoa(selected), 1)
		list.SetSelectedIndex(selected)
		lines := direct.Render(width)
		annotated := strings.TrimSuffix(view, "}") + `,"width":72}`
		pushes := map[string]*WidgetPushPayload{
			"lines":         {Key: "kit", Lines: lines, Width: width},
			"annotated":     {Key: "kit", Lines: lines, Width: width, View: json.RawMessage(annotated)},
			"authoritative": {Key: "kit", View: json.RawMessage(view)},
		}
		var want string
		for i, term := range terminals {
			term.out.Reset()
			term.bridge.HandleWidgetPush("fixture", nil, pushes[term.name])
			if frame == 0 {
				term.bridge.mu.RLock()
				proxy := term.bridge.widgets["fixture:kit"]
				term.bridge.mu.RUnlock()
				term.ui.Add(proxy)
			}
			term.ui.Render()
			got := term.out.String()
			if term.name == "annotated" && term.bridge.widgets["fixture:kit"].FrontendView(width) == nil {
				t.Fatalf("frame %d: the annotated view was dropped, so the comparison proves nothing", frame)
			}
			if i == 0 {
				if !strings.Contains(got, "Track") {
					t.Fatalf("frame %d: lines terminal wrote %q", frame, got)
				}
				want = got
				continue
			}
			if got != want {
				t.Fatalf("frame %d: %s terminal wrote\n%q\nwant the lines terminal's\n%q", frame, term.name, got, want)
			}
		}
	}
}
