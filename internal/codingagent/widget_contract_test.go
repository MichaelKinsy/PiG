package codingagent

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/extensions/sdk/tui/widthx"
	"github.com/MichaelKinsy/PiG/tui"
)

// Boundary obligations of renderWidgets and renderWidgetContainer (packages/coding-agent/src/modes/interactive/interactive-mode.ts):
// each side lists its widgets in the order they were set across the extension processes and the host process, a replaced or moved
// widget takes the end of its side, a key is one namespace, the above side always starts with a blank row and the below side has none,
// and a change requests a render.

func widgetContractMode() *InteractiveMode {
	return &InteractiveMode{widgetContainer: tui.NewContainer(), widgetContainerBelow: tui.NewContainer()}
}

func setBridgeWidget(t *testing.T, bridge *subprocess.UIBridge, args string) {
	t.Helper()
	if _, err := bridge.HandleCall("probe", &subprocess.CallPayload{Method: "ui.setWidget", Args: json.RawMessage(args)}); err != nil {
		t.Fatal(err)
	}
}

func contractWidgetRows(c *tui.Container) []string {
	rows := trimRows(c.Render(40))
	for i, row := range rows {
		rows[i] = widthx.StripAnsi(row)
	}
	return rows
}

// Several extension-process widgets that first reach the layout in one sync take their places in the order the bridge set them.
func TestWidgetsFirstSeenTogetherKeepTheBridgeOrder(t *testing.T) {
	m := widgetContractMode()
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	var snapshot map[string]*subprocess.PushProxy
	bridge.SetWidgetSyncFunc(func(widgets map[string]*subprocess.PushProxy) { snapshot = widgets })
	keys := []string{"w-a", "w-b", "w-c", "w-d", "w-e", "w-f", "w-g", "w-h"}
	for _, key := range keys {
		setBridgeWidget(t, bridge, `{"key":"`+key+`","content":["`+key+`"]}`)
	}
	m.syncWidgets(snapshot)
	want := append([]string{""}, keys...)
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, want) {
		t.Fatalf("above rows = %q, want %q", got, want)
	}
}

// A widget the extension moves to the other side and back takes the end of each side it lands on, behind the widgets set meanwhile.
func TestMovedBridgeWidgetTakesTheEndOfItsSide(t *testing.T) {
	m := widgetContractMode()
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	bridge.SetWidgetSyncFunc(m.syncWidgets)
	setBridgeWidget(t, bridge, `{"key":"x","content":["x-widget"]}`)
	setBridgeWidget(t, bridge, `{"key":"y","content":["y-widget"]}`)
	setBridgeWidget(t, bridge, `{"key":"z","content":["z-below"],"options":{"placement":"belowEditor"}}`)
	steps := []struct {
		args         string
		above, below []string
	}{
		{`{"key":"x","content":["x-widget"],"width":40,"options":{"placement":"belowEditor"}}`, []string{"", "y-widget"}, []string{"z-below", "x-widget"}},
		{`{"key":"x","content":["x-widget"],"width":40,"options":{"placement":"aboveEditor"}}`, []string{"", "y-widget", "x-widget"}, []string{"z-below"}},
	}
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, []string{"", "x-widget", "y-widget"}) {
		t.Fatalf("initial above rows = %q", got)
	}
	for i, step := range steps {
		setBridgeWidget(t, bridge, step.args)
		if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, step.above) {
			t.Fatalf("step %d: above rows = %q, want %q", i, got, step.above)
		}
		if got := contractWidgetRows(m.widgetContainerBelow); !reflect.DeepEqual(got, step.below) {
			t.Fatalf("step %d: below rows = %q, want %q", i, got, step.below)
		}
	}
}

type disposableWidget struct {
	*tui.Text
	disposed *bool
}

func (d *disposableWidget) Dispose() { *d.disposed = true }

// Widget keys are one namespace: an extension process that sets a key after a host-process extension did replaces that widget and
// disposes it, and a host-process widget set later replaces the process one.
func TestWidgetKeysAreOneNamespaceAcrossHosts(t *testing.T) {
	m := widgetContractMode()
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	bridge.SetWidgetSyncFunc(m.syncWidgets)
	var disposed bool
	m.setExtensionWidget("shared", &disposableWidget{Text: tui.NewText("host-first"), disposed: &disposed}, nil)
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, []string{"", "host-first"}) {
		t.Fatalf("host widget rows = %q", got)
	}
	setBridgeWidget(t, bridge, `{"key":"shared","content":["process-second"]}`)
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, []string{"", "process-second"}) {
		t.Fatalf("after the process set the key: rows = %q, want only its widget", got)
	}
	if !disposed {
		t.Fatal("the replaced host widget was not disposed")
	}
	m.setExtensionWidget("shared", tui.NewText("host-third"), nil)
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, []string{"", "host-third"}) {
		t.Fatalf("after the host set the key again: rows = %q, want only its widget", got)
	}
}

// Both sides lay out empty: the above side is a blank row, the below side nothing; a below widget adds no blank row.
func TestEmptyAndBelowWidgetSidesHaveTheUpstreamSpacers(t *testing.T) {
	m := widgetContractMode()
	m.syncWidgets(nil)
	if got := m.widgetContainer.Render(20); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("empty above side = %q, want one blank row", got)
	}
	if got := m.widgetContainerBelow.Render(20); len(got) != 0 {
		t.Fatalf("empty below side = %q, want no rows", got)
	}
	bridge := subprocess.NewUIBridge(func() {})
	bridge.SetUIContext(&ExtUIContext{m: m})
	bridge.SetWidgetSyncFunc(m.syncWidgets)
	setBridgeWidget(t, bridge, `{"key":"b","content":["below-one"],"options":{"placement":"belowEditor"}}`)
	if got := contractWidgetRows(m.widgetContainerBelow); !reflect.DeepEqual(got, []string{"below-one"}) {
		t.Fatalf("below side = %q, want the widget without a blank row", got)
	}
	if got := contractWidgetRows(m.widgetContainer); !reflect.DeepEqual(got, []string{""}) {
		t.Fatalf("above side with only a below widget = %q, want one blank row", got)
	}
}

// Setting or clearing a widget requests a render (renderWidgets ends with ui.requestRender()).
func TestSettingAWidgetRequestsARender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m, term := newTickRenderProbe(t, "regular")
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		m.drainMainLoopOnce()
		term.take()
		m.setExtensionWidget("probe", tui.NewText("probe-widget-text"), nil)
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		m.drainMainLoopOnce()
		if frame := widthx.StripAnsi(term.take()); !strings.Contains(frame, "probe-widget-text") {
			t.Fatalf("the widget was not painted after it was set: %q", frame)
		}
	})
}

// A widget set before the layout containers exist is kept without a layout, and does not panic.
func TestWidgetSetBeforeTheContainersExistDoesNotPanic(t *testing.T) {
	m := &InteractiveMode{}
	m.setExtensionWidget("early", tui.NewText("early-widget"), nil)
	m.syncWidgets(nil)
}
