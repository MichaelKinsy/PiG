package extensionconformance

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
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
	{"pi-coding-agent", "SkillInvocationMessageComponent"},
	{"pi-coding-agent", "CustomMessageComponent"},
	{"pi-tui", "CancellableLoader"},
	{"pi-coding-agent", "BorderedLoader"},
	{"pi-tui", "Input"},
	{"pi-tui", "Editor"},
	{"pi-tui", "ScrollView"},
	{"pi-tui", "MouseRegion"},
}

// kitTerminalRows is the terminal height the host reports to each cell (a harness's); a case sets it before it opens.
var kitTerminalRows sync.Map // *harness -> *atomic.Int32

func kitRowsOf(h *harness) *atomic.Int32 {
	rows, _ := kitTerminalRows.LoadOrStore(h, new(atomic.Int32))
	return rows.(*atomic.Int32)
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
		// Rows is the terminal's height, which an editor's visible line count follows; Pi's cases default to 24.
		Rows int `json:"rows"`
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
	rows := spec.Rows
	if rows == 0 {
		rows = 24
	}
	kitRowsOf(h).Store(int32(rows))
	if h.host != nil {
		h.host.NotifyHeight(rows)
	}
	done := make(chan error, 1)
	go func() { done <- command.Handler(kitCommandContext(t, h), key) }()
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
	// The probe reports each callback as it fires and never closes itself: a probe that closed after the callbacks Pi
	// fired would drop the steps still to come (a press fires the region's callback, and the click that follows
	// is the child's). The case ends with its overlay's timeout, as the cases without callbacks do.
	var got []string
	awaitCondition(ctx, func() bool { got = kitWidgetCallbacks(h); return len(got) >= len(events) })
	if !slices.Equal(got, events) {
		t.Fatalf("%s %q at width %d: callbacks = %q, Pi fired %q", key, c.Name, width, got, events)
	}
}

// kitWidgetCallbacks are the callbacks the cell's nodes have fired since the case opened, in order: the probe
// notifies "kit-widget-event=<type>:<value>" for each (a change report, a key or a mouse event it was handed is not one).
func kitWidgetCallbacks(h *harness) []string {
	var got []string
	for _, message := range h.ui.Recorded() {
		if entry, ok := strings.CutPrefix(message, "kit-widget-event="); ok {
			got = append(got, strings.TrimSuffix(entry, ":info"))
		}
	}
	return got
}

func TestConformance_ComponentKitWidgetsMatchPi(t *testing.T) {
	// Pi's rows are a loader's frames at the instant the vector was taken; the host's own animation is frozen.
	subprocess.SetViewLoaderAnimation(false)
	t.Cleanup(func() { subprocess.SetViewLoaderAnimation(true) })
	t.Parallel()
	for _, tc := range componentKitSDKCells() {
		t.Run(tc.name, func(t *testing.T) {
			// The cells are independent processes; a vector file's cases take minutes in each.
			t.Parallel()
			h := startKitCell(t, tc)
			ui := newKitUI(h)
			for _, v := range kitWidgetExports {
				// One subtest per export, so -run selects an export's vectors; a failing export stops the cell, as its
				// open overlay would fail the next.
				if !t.Run(v.export, func(t *testing.T) {
					for _, c := range readKitWidgetVectors(t, v.dir, v.export) {
						if c.Throws != "" {
							t.Fatalf("%s %q: Pi threw %s", v.export, c.Name, c.Throws)
						}
						for _, frame := range c.Result.Frames {
							runKitWidget(t, h, ui, tc, v.dir+"/"+v.export+"/"+c.Name, c, frame.Width, frame.States, frame.Events)
						}
					}
				}) {
					return
				}
			}
		})
	}
}
