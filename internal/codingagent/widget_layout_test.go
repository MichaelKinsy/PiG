package codingagent

import (
	"maps"
	"reflect"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// Pi 0.87.1 interactive-mode.ts:2401-2422 puts Spacer(1) before the
// above-editor widgets, even when empty. editor.ts:556-613 starts at its border.
func TestAboveEditorWidgetSpacerPrecedesWidgets(t *testing.T) {
	m := &InteractiveMode{widgetContainer: tui.NewContainer()}
	widget := subprocess.NewPushProxy(nil, nil)
	widget.UpdateLines([]string{"widget"})
	for _, widgets := range []map[string]*subprocess.PushProxy{nil, {"test": widget}, nil} {
		m.syncWidgets(widgets)
		got := m.widgetContainer.Render(20)
		want := []string{""}
		if len(widgets) != 0 {
			want = append(want, "widget")
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("above-editor rows = %q, want %q", got, want)
		}
		editor := tui.NewEditor()
		rows := tui.NewContainer(m.widgetContainer, editor).Render(20)
		if got := widthx.StripAnsi(rows[len(want)]); got != strings.Repeat("─", 20) {
			t.Fatalf("row after widgets = %q, want editor border", got)
		}
	}
}

// interactive-mode.ts renderWidgets lays out extensionWidgetsAbove and extensionWidgetsBelow, one Map per side in the order the widgets were set
// (setExtensionWidget deletes a key before setting it, so a replaced widget moves to the end). Pig keeps the extension processes' widgets and the
// host-process ones apart, so the layout must interleave the two by the order they were set.
func TestWidgetsFollowTheOrderTheyWereSetAcrossExtensionHosts(t *testing.T) {
	m := &InteractiveMode{widgetContainer: tui.NewContainer(), widgetContainerBelow: tui.NewContainer()}
	proxy := func(text string) *subprocess.PushProxy {
		p := subprocess.NewPushProxy(nil, nil)
		p.UpdateLines([]string{text})
		return p
	}
	host := func(text string) tui.Component { return tui.NewText(text) }
	remote := map[string]*subprocess.PushProxy{}
	steps := []struct {
		apply        func()
		above, below []string
	}{
		{func() { m.setExtensionWidget("a", host("host a"), nil) }, []string{"", "host a"}, nil},
		{func() { remote["x"] = proxy("remote x"); m.syncWidgets(maps.Clone(remote)) }, []string{"", "host a", "remote x"}, nil},
		{func() { m.setExtensionWidget("b", host("host b"), nil) }, []string{"", "host a", "remote x", "host b"}, nil},
		{func() { m.setExtensionWidget("a", host("host a2"), nil) }, []string{"", "remote x", "host b", "host a2"}, nil},
		{func() { remote["x"] = proxy("remote x2"); m.syncWidgets(maps.Clone(remote)) }, []string{"", "host b", "host a2", "remote x2"}, nil},
		{func() {
			m.setExtensionWidget("c", host("host c"), &extension.ExtensionWidgetOptions{Placement: extension.WidgetPlacementBelowEditor})
		}, []string{"", "host b", "host a2", "remote x2"}, []string{"host c"}},
		{func() { remote["y"] = proxy("remote y"); m.syncWidgets(maps.Clone(remote)) }, []string{"", "host b", "host a2", "remote x2", "remote y"}, []string{"host c"}},
		{func() { delete(remote, "x"); m.syncWidgets(maps.Clone(remote)) }, []string{"", "host b", "host a2", "remote y"}, []string{"host c"}},
	}
	for i, step := range steps {
		step.apply()
		if got := trimRows(m.widgetContainer.Render(20)); !reflect.DeepEqual(got, step.above) {
			t.Fatalf("step %d: above = %q, want %q", i, got, step.above)
		}
		if got := trimRows(m.widgetContainerBelow.Render(20)); !reflect.DeepEqual(got, step.below) {
			t.Fatalf("step %d: below = %q, want %q", i, got, step.below)
		}
	}
}

func trimRows(rows []string) []string {
	var out []string
	for _, row := range rows {
		out = append(out, strings.TrimSpace(row))
	}
	return out
}

func BenchmarkAboveEditorWidgetLayout(b *testing.B) {
	m := &InteractiveMode{widgetContainer: tui.NewContainer()}
	widget := subprocess.NewPushProxy(nil, nil)
	widget.UpdateLines([]string{"one", "two", "three"})
	m.syncWidgets(map[string]*subprocess.PushProxy{"test": widget})
	layout := tui.NewContainer(m.widgetContainer, tui.NewEditor())
	b.ReportAllocs()
	for b.Loop() {
		layout.Render(120)
	}
}
