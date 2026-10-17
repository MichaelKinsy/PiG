package codingagent

import (
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/tui"
)

func newWidgetMode(t *testing.T) (*InteractiveMode, *ExtUIContext) {
	t.Helper()
	m, _ := newCustomEditorDispatchMode(t)
	m.uiTaskCh = make(chan func(), 64)
	m.widgetContainer = tui.NewContainer(tui.NewSpacer(1))
	m.widgetContainerBelow = tui.NewContainer()
	return m, &ExtUIContext{m: m}
}

func widgetRows(c *tui.Container) string { return strings.Join(c.Render(40), "|") }

// interactive-mode.ts:2398-2438 setExtensionWidget: a string array becomes one text row per line (at most MAX_WIDGET_LINES = 10, then a muted
// "... (widget truncated)" row) above the editor by default or below it with placement "belowEditor"; setting a key again replaces its widget
// wherever it was; undefined removes it.
func TestExtUIContextSetWidgetShowsLinesAboveOrBelowTheEditor(t *testing.T) {
	m, ui := newWidgetMode(t)
	ui.SetWidget("a", []string{"alpha", "beta"}, nil)
	ui.SetWidget("b", []string{"below"}, &extension.ExtensionWidgetOptions{Placement: extension.WidgetPlacementBelowEditor})
	runPostedTasks(t, m)
	above, below := widgetRows(m.widgetContainer), widgetRows(m.widgetContainerBelow)
	if !strings.Contains(above, " alpha") || !strings.Contains(above, " beta") || strings.Contains(above, "below") || !strings.Contains(below, " below") {
		t.Fatalf("above %q below %q", above, below)
	}

	var many []string
	for i := range 12 {
		many = append(many, "line"+string(rune('A'+i)))
	}
	ui.SetWidget("a", many, nil)
	runPostedTasks(t, m)
	above = widgetRows(m.widgetContainer)
	if !strings.Contains(above, "lineJ") || strings.Contains(above, "lineK") || !strings.Contains(above, "... (widget truncated)") || strings.Contains(above, "alpha") {
		t.Fatalf("12 lines above = %q, want the first 10 and the truncation notice, replacing the old widget", above)
	}

	ui.SetWidget("a", []string{"moved"}, &extension.ExtensionWidgetOptions{Placement: extension.WidgetPlacementBelowEditor})
	ui.SetWidget("b", nil, nil)
	runPostedTasks(t, m)
	above, below = widgetRows(m.widgetContainer), widgetRows(m.widgetContainerBelow)
	if strings.Contains(above, "moved") || !strings.Contains(below, " moved") || strings.Contains(below, "below") {
		t.Fatalf("after moving a and removing b: above %q below %q", above, below)
	}
}

// .upstream/current/packages/coding-agent/src/core/extensions/types.ts:189 setWidget(key, factory, options): the host calls the factory with (ui, theme), shows the component it returns, and calls its dispose
// when the widget is replaced, removed or cleared (.upstream/current/packages/coding-agent/src/modes/interactive/interactive-mode.ts:2404-2411, 2440-2450).
func TestExtUIContextSetWidgetFactoryBuildsAndDisposesComponents(t *testing.T) {
	m, concrete := newWidgetMode(t)
	var ui extension.UIContext = concrete
	first := &widthComponent{prefix: "w1:"}
	var gotHost tui.TUI
	var gotTheme *tui.Theme
	ui.SetWidgetFactory("w", func(host tui.TUI, theme *tui.Theme) extension.DisposableComponent {
		gotHost, gotTheme = host, theme
		return first
	}, nil)
	runPostedTasks(t, m)
	if gotHost != m.tuiInst || gotTheme == nil {
		t.Fatalf("factory got host=%v theme=%v, want the TUI and the active theme", gotHost, gotTheme)
	}
	if rows := m.widgetContainer.Render(5); !slices.Contains(rows, "w1:xxxxx") {
		t.Fatalf("widget rows = %q, want the component rendered at the container's width", rows)
	}

	second := &widthComponent{prefix: "w2:"}
	ui.SetWidgetFactory("w", func(tui.TUI, *tui.Theme) extension.DisposableComponent { return second }, &extension.ExtensionWidgetOptions{Placement: extension.WidgetPlacementBelowEditor})
	runPostedTasks(t, m)
	if first.disposed != 1 || second.disposed != 0 || slices.Contains(m.widgetContainer.Render(3), "w1:xxx") || !slices.Contains(m.widgetContainerBelow.Render(3), "w2:xxx") {
		t.Fatalf("after replacing: disposed %d/%d, above %q, below %q", first.disposed, second.disposed, m.widgetContainer.Render(3), m.widgetContainerBelow.Render(3))
	}

	ui.SetWidgetFactory("w", nil, nil)
	runPostedTasks(t, m)
	if second.disposed != 1 || len(m.inprocessWidgets) != 0 {
		t.Fatalf("after nil: disposed %d, widgets %d", second.disposed, len(m.inprocessWidgets))
	}

	third, fourth := &widthComponent{prefix: "w3:"}, &widthComponent{prefix: "w4:"}
	ui.SetWidgetFactory("x", func(tui.TUI, *tui.Theme) extension.DisposableComponent { return third }, nil)
	ui.SetWidgetFactory("y", func(tui.TUI, *tui.Theme) extension.DisposableComponent { return fourth }, nil)
	runPostedTasks(t, m)
	m.clearExtensionWidgets()
	if third.disposed != 1 || fourth.disposed != 1 || len(m.inprocessWidgets) != 0 || slices.Contains(m.widgetContainer.Render(3), "w3:xxx") {
		t.Fatalf("clear: disposed %d/%d, widgets %d", third.disposed, fourth.disposed, len(m.inprocessWidgets))
	}
}
