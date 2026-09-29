package codingagent

import (
	"reflect"
	"strings"
	"testing"

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
