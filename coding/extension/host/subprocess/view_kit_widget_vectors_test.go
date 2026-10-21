package subprocess

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
	"github.com/MichaelKinsy/PiG/tui"
)

// widgetVectorExports are the Pi exports whose vectors are rendered kit views (automation/gen/sdk-vector-lib/kit-build.mjs):
// each case's rows are what Pi's own component draws under Pi's dark theme at 24-bit color, at every width, before and after
// each step of the case.
var widgetVectorExports = []struct{ dir, export string }{
	{"pi-coding-agent", "BranchSummaryMessageComponent"},
	{"pi-coding-agent", "CompactionSummaryMessageComponent"},
	{"pi-coding-agent", "SkillInvocationMessageComponent"},
	{"pi-coding-agent", "CustomMessageComponent"},
	{"pi-tui", "CancellableLoader"},
	{"pi-coding-agent", "BorderedLoader"},
	{"pi-tui", "Input"},
	{"pi-tui", "Editor"},
	{"pi-tui", "ScrollView"},
	{"pi-tui", "MouseRegion"},
}

// widgetVectorSpec is a case's argument: the wire node, the steps delivered to the focused node or the pointer, and the
// focused node's id.
type widgetVectorSpec struct {
	Root  json.RawMessage `json:"root"`
	Focus string          `json:"focus"`
	// Rows is the terminal's height, which an editor's visible line count follows; Pi's cases default to 24.
	Rows  int `json:"rows"`
	Steps []struct {
		Keys  *string `json:"keys"`
		Mouse *struct {
			Type, Button string
			X, Y         int
			ScreenX      *int
			ScreenY      *int
			Shift        bool
			Alt          bool
			Ctrl         bool
			WheelDelta   int
			ClickCount   int
		} `json:"mouse"`
	} `json:"steps"`
}

type widgetVectorFile struct {
	Cases []struct {
		Name   string
		Args   []json.RawMessage
		Result struct {
			Frames []struct {
				Width  int
				States [][]string
				// Events are the callbacks Pi's nodes fired, "<type>:<value>".
				Events []string
				// Changes are a scroll view's state after the render and each step.
				Changes []string
				// Results are what each mouse step was answered.
				Results []string
			}
		}
		Throws string
	}
}

func readWidgetVectors(t *testing.T, dir, export string) widgetVectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "parity", "sdk-vectors", dir, export+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file widgetVectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s has no cases", export)
	}
	return file
}

// viewSurfaceForVector accepts the case's view on a surface that takes events.
func viewSurfaceForVector(t *testing.T, spec widgetVectorSpec) (*viewSurface, *[]ViewEventPayload) {
	t.Helper()
	s := newViewSurface("k", newViewImageStore())
	var events []ViewEventPayload
	s.sendEvent = func(ev ViewEventPayload) { events = append(events, ev) }
	s.repaint = func() {}
	rows := spec.Rows
	if rows == 0 {
		rows = 24
	}
	s.terminalRows = func() int { return rows }
	payload, err := json.Marshal(map[string]any{"root": spec.Root, "focus": spec.Focus})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.accept(payload, nil); err != nil {
		t.Fatalf("view rejected: %v", err)
	}
	t.Cleanup(s.close)
	return s, &events
}

func TestWidgetVectorsRenderLikePi(t *testing.T) {
	previous := tui.ActiveTheme()
	SetViewLoaderAnimation(false)
	t.Cleanup(func() { SetViewLoaderAnimation(true) })
	t.Setenv("COLORTERM", "truecolor")
	tui.ResetCapabilitiesCache()
	t.Cleanup(func() { tui.ResetCapabilitiesCache(); tui.SetThemeInstance(previous) })
	tui.SetTheme("dark")
	for _, v := range widgetVectorExports {
		for _, c := range readWidgetVectors(t, v.dir, v.export).Cases {
			if c.Throws != "" {
				t.Fatalf("%s %s: Pi threw %s", v.export, c.Name, c.Throws)
			}
			var spec widgetVectorSpec
			if err := json.Unmarshal(c.Args[0], &spec); err != nil {
				t.Fatal(err)
			}
			for _, frame := range c.Result.Frames {
				s, sent := viewSurfaceForVector(t, spec)
				rows := s.Render(frame.Width)
				if !slices.Equal(rows, frame.States[0]) {
					t.Fatalf("%s %q at %d:\n got %q\nwant %q", v.export, c.Name, frame.Width, rows, frame.States[0])
				}
				var scrollStates []string
				var mouseResults []string
				recordScroll := func() {
					if state := scrollStateOf(s.FrontendView(frame.Width)); state != "" {
						scrollStates = append(scrollStates, state)
					}
				}
				recordScroll()
				for i, step := range spec.Steps {
					switch {
					case step.Keys != nil:
						s.HandleViewInput(*step.Keys)
					case step.Mouse != nil:
						m := step.Mouse
						ev := extension.RemoteMouseEvent{Type: m.Type, Button: m.Button, X: m.X, Y: m.Y, ScreenX: m.X, ScreenY: m.Y, Width: frame.Width, Height: len(rows),
							Shift: m.Shift, Alt: m.Alt, Ctrl: m.Ctrl, WheelDelta: m.WheelDelta, ClickCount: m.ClickCount}
						if m.ScreenX != nil {
							ev.ScreenX = *m.ScreenX
						}
						if m.ScreenY != nil {
							ev.ScreenY = *m.ScreenY
						}
						result := s.HandleViewMouse(ev)
						mouseResults = append(mouseResults, mouseResultFlags(result))
					}
					rows = s.Render(frame.Width)
					recordScroll()
					if !slices.Equal(rows, frame.States[i+1]) {
						t.Fatalf("%s %q at %d after step %d:\n got %q\nwant %q", v.export, c.Name, frame.Width, i, rows, frame.States[i+1])
					}
				}
				if got := viewCallbacks(*sent); !slices.Equal(got, frame.Events) {
					t.Fatalf("%s %q at %d: callbacks = %q, Pi fired %q", v.export, c.Name, frame.Width, got, frame.Events)
				}
				if len(frame.Results) > 0 && !slices.Equal(mouseResults, frame.Results) {
					t.Fatalf("%s %q at %d: mouse results = %q, Pi answered %q", v.export, c.Name, frame.Width, mouseResults, frame.Results)
				}
				if len(frame.Changes) > 0 && v.export == "ScrollView" && !slices.Equal(scrollStates, frame.Changes) {
					t.Fatalf("%s %q at %d: scroll state = %q, Pi's is %q", v.export, c.Name, frame.Width, scrollStates, frame.Changes)
				}
			}
		}
	}
}

// viewCallbacks are the events the extension received for Pi's callbacks, as the vectors name them: "<type>:<value>",
// and "mouse:<type>/<button>/<x>/<y>" for a mouse region; the change reports are the host's own.
func viewCallbacks(events []ViewEventPayload) []string {
	var out []string
	for _, ev := range events {
		switch {
		case ev.Type == ViewEventChange:
		case ev.Type == ViewEventMouse && ev.Mouse != nil:
			out = append(out, fmt.Sprintf("mouse:%s/%s/%d/%d", ev.Mouse.Type, ev.Mouse.Button, ev.Mouse.X, ev.Mouse.Y))
		default:
			out = append(out, ev.Type+":"+ev.Value)
		}
	}
	return out
}

// mouseResultFlags is what the renderer is answered: "h" handled, "c" capture, "f" focus, "-" none.
func mouseResultFlags(r extension.ViewMouseResult) string {
	flags := ""
	if r.Handled {
		flags += "h"
	}
	if r.Capture {
		flags += "c"
	}
	if r.Focus {
		flags += "f"
	}
	if flags == "" {
		return "-"
	}
	return flags
}

// scrollStateOf is the first scroll view's state in the frontend view, as the vectors' probe names it
// ("<scrollTop>/<viewportHeight>/<followingEnd>/<scrollbarVisible>"), or "".
func scrollStateOf(view *frontend.View) string {
	if view == nil {
		return ""
	}
	var find func(n *frontend.ViewNode) string
	find = func(n *frontend.ViewNode) string {
		if n.Scroll != nil {
			return fmt.Sprintf("%d/%d/%t/%t", n.Scroll.Top, n.Scroll.ViewportHeight, n.Scroll.FollowingEnd, n.Scroll.ScrollbarVisible)
		}
		for i := range n.Children {
			if state := find(&n.Children[i]); state != "" {
				return state
			}
		}
		return ""
	}
	return find(&view.Root)
}
