package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Widget kind conformance (D107, docs/plan/extension-component-kit.md §2.2, §10).
//
// The reference is Pi itself: test/parity/sdk-vectors/<module>/<export>.json holds the rows Pi's own component draws
// for a case's wire node at every width, before and after each step (keys and mouse events), and the callbacks its
// nodes fired. Every SDK cell builds the case with its own constructors in the kit-widget command, and the host
// draws it: the rows must equal Pi's byte for byte.

// kitWidgetExports are the Pi exports whose vectors are rendered widget cases.
var kitWidgetExports = []struct{ dir, export string }{
	{"pi-coding-agent", "BranchSummaryMessageComponent"},
	{"pi-coding-agent", "CompactionSummaryMessageComponent"},
}

type kitWidgetCase struct {
	Name   string
	Args   []json.RawMessage
	Result struct {
		Frames []struct {
			Width  int
			States [][]string
			// Events are the callbacks Pi's nodes fired, "<type>:<value>", in order.
			Events []string
			// Changes are the values an input or an editor held after each step.
			Changes []string
		}
	}
	Throws string
}

type kitWidgetStep struct {
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
}

func readKitWidgetVectors(t *testing.T, dir, export string) []kitWidgetCase {
	t.Helper()
	root := findModuleRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "test", "parity", "sdk-vectors", dir, export+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct{ Cases []kitWidgetCase }
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Cases) == 0 {
		t.Fatalf("%s has no cases", export)
	}
	return file.Cases
}

// runKitWidget opens one case in a cell and checks every width's rows against Pi's, delivering the case's steps as
// the terminal does.
func runKitWidget(t *testing.T, h *harness, ui *kitUI, tc kitCell, key string, c kitWidgetCase, width int, states [][]string, events []string) {
	t.Helper()
	var steps []kitWidgetStep
	var spec struct {
		Steps []kitWidgetStep `json:"steps"`
	}
	if err := json.Unmarshal(c.Args[0], &spec); err != nil {
		t.Fatal(err)
	}
	steps = spec.Steps
	command, ok := findCommand(h.runner, "kit-widget")
	if !ok {
		t.Fatal("kit-widget is not registered")
	}
	h.ui.ClearRecorded()
	done := make(chan error, 1)
	args := key
	if len(events) > 0 {
		args = fmt.Sprintf("%s %d", key, len(events))
	}
	go func() { done <- command.Handler(kitCommandContext(t, h), args) }()
	var session *kitSession
	select {
	case session = <-ui.sessions:
	case err := <-done:
		t.Fatalf("kit-widget %s returned before it showed the case: %v", key, err)
	case <-time.After(10 * time.Second):
		t.Fatalf("kit-widget %s never showed the case", key)
	}
	if h.host != nil {
		h.host.NotifyWidth(width)
	}
	ctx := testbudget.Context(t)
	expect := func(step string, want []string) {
		var got []string
		if !awaitCondition(ctx, func() bool { got = session.rows(width); return slices.Equal(got, want) }) {
			t.Fatalf("%s %q at width %d, %s:\n got %q\nwant %q", key, c.Name, width, step, got, want)
		}
	}
	expect("first frame", states[0])
	for i, step := range steps {
		switch {
		case step.Keys != nil:
			session.input(*step.Keys)
		case step.Mouse != nil:
			m := step.Mouse
			event := extension.RemoteMouseEvent{Type: m.Type, Button: m.Button, X: m.X, Y: m.Y, ScreenX: m.X, ScreenY: m.Y, Width: width,
				Height: len(states[i]), Shift: m.Shift, Alt: m.Alt, Ctrl: m.Ctrl, WheelDelta: m.WheelDelta, ClickCount: m.ClickCount}
			if m.ScreenX != nil {
				event.ScreenX = *m.ScreenX
			}
			if m.ScreenY != nil {
				event.ScreenY = *m.ScreenY
			}
			if session.mouse == nil {
				t.Fatalf("%s: the cell takes no mouse", tc.name)
			}
			session.mouse(event)
		}
		expect(fmt.Sprintf("after step %d", i+1), states[i+1])
	}
	if len(events) > 0 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("kit-widget %s: %v", key, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("kit-widget %s %q did not close after %d callbacks", key, c.Name, len(events))
		}
		// The log holds the callbacks and, between them, the host's change reports; the callbacks are compared.
		var got []string
		for _, message := range h.ui.Recorded() {
			if log, ok := strings.CutPrefix(message, "kit-widget="); ok {
				for _, entry := range strings.Split(strings.TrimSuffix(log, ":info"), ",") {
					if !strings.HasPrefix(entry, "change:") && !strings.HasPrefix(entry, "rawmouse:") && !strings.HasPrefix(entry, "input:") {
						got = append(got, entry)
					}
				}
			}
		}
		if !slices.Equal(got, events) {
			t.Fatalf("%s %q at width %d: callbacks = %q, Pi fired %q", key, c.Name, width, got, events)
		}
	}
}

func TestConformance_ComponentKitWidgetsMatchPi(t *testing.T) {
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			h := startKitCell(t, tc)
			ui := newKitUI(h)
			for _, v := range kitWidgetExports {
				for _, c := range readKitWidgetVectors(t, v.dir, v.export) {
					if c.Throws != "" {
						t.Fatalf("%s %q: Pi threw %s", v.export, c.Name, c.Throws)
					}
					for _, frame := range c.Result.Frames {
						runKitWidget(t, h, ui, tc, v.dir+"/"+v.export+"/"+c.Name, c, frame.Width, frame.States, frame.Events)
					}
				}
			}
		})
	}
}
