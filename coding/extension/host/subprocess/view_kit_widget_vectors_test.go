package subprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

// widgetVectorExports are the Pi exports whose vectors are rendered kit views (automation/gen/sdk-vector-lib/kit-build.mjs):
// each case's rows are what Pi's own component draws under Pi's dark theme at 24-bit color, at every width, before and after
// each step of the case.
var widgetVectorExports = []struct{ dir, export string }{
	{"pi-coding-agent", "BranchSummaryMessageComponent"},
	{"pi-coding-agent", "CompactionSummaryMessageComponent"},
}

// widgetVectorSpec is a case's argument: the wire node, the steps delivered to the focused node or the pointer, and the
// focused node's id.
type widgetVectorSpec struct {
	Root  json.RawMessage `json:"root"`
	Focus string          `json:"focus"`
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
				s, _ := viewSurfaceForVector(t, spec)
				rows := s.Render(frame.Width)
				if !slices.Equal(rows, frame.States[0]) {
					t.Fatalf("%s %q at %d:\n got %q\nwant %q", v.export, c.Name, frame.Width, rows, frame.States[0])
				}
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
						s.HandleViewMouse(ev)
					}
					rows = s.Render(frame.Width)
					if !slices.Equal(rows, frame.States[i+1]) {
						t.Fatalf("%s %q at %d after step %d:\n got %q\nwant %q", v.export, c.Name, frame.Width, i, rows, frame.States[i+1])
					}
				}
			}
		}
	}
}
