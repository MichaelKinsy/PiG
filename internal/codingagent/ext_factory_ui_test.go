package codingagent

import (
	"context"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/tui"
)

// A compiled-in extension is an extension.ExtensionFactory (types.ts:1566). Pi's loader hands it createExtensionAPI's pi, and its handlers reach the
// interactive mode through ctx.ui (types.ts:143-300). This drives that whole path: the real factoryload, a real inproc Runner bound to the mode's
// ExtUIContext, and a session_start handler that installs a component factory in every member of ExtensionUIContext that takes one.
func TestCompiledInFactoryReachesEveryUIFactoryMemberThroughTheLoaderAndTheInteractiveMode(t *testing.T) {
	m, ui := newWidgetMode(t)
	m.extHeader = newSpecialLinesComponent(func() {})
	m.extFooter = newSpecialLinesComponent(func() {})
	m.statusLine = NewFooterComponent(nil, "", nil)

	header, footer, widget := &widthComponent{prefix: "h:"}, &widthComponent{prefix: "f:"}, &widthComponent{prefix: "w:"}
	editor := &factoryEditor{}
	var handlerUI extension.UIContext
	var installed extension.EditorFactory
	runtime := extension.CreateExtensionRuntime()
	loaded, err := factoryload.LoadExtensionFromFactory(func(pi extension.API) error {
		pi.OnSessionStart(func(ctx context.Context, _ extension.SessionStartEvent) error {
			ctxUI, err := extension.FromContext(ctx).UI()
			if err != nil {
				return err
			}
			ctxUI.SetHeader(func(tui.TUI, *tui.Theme) extension.DisposableComponent { return header })
			ctxUI.SetFooter(func(tui.TUI, *tui.Theme, extension.ReadonlyFooterDataProvider) extension.DisposableComponent {
				return footer
			})
			ctxUI.SetWidgetFactory("w", func(tui.TUI, *tui.Theme) extension.DisposableComponent { return widget }, nil)
			installed = func(tui.TUI, tui.EditorTheme, extension.KeybindingsManager) tui.EditorComponent { return editor }
			ctxUI.SetEditorComponent(installed)
			handlerUI = ctxUI
			return nil
		})
		return nil
	}, ".", extension.CreateEventBus(), runtime, "<compiled-in>")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{loaded}, ".", runtime)
	runner.SetUIContext(ui, extension.ModeTUI)

	if _, err := runner.Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"}); err != nil {
		t.Fatal(err)
	}
	runPostedTasks(t, m)

	if got := m.extHeader.Render(3); !slices.Equal(got, []string{"h:xxx"}) {
		t.Fatalf("header rows = %q, want the factory's component", got)
	}
	if got := m.extFooter.Render(3); !slices.Equal(got, []string{"f:xxx"}) {
		t.Fatalf("footer rows = %q, want the factory's component", got)
	}
	if rows := m.widgetContainer.Render(3); !slices.Contains(rows, "w:xxx") {
		t.Fatalf("widget rows = %q, want the factory's component", rows)
	}
	if !m.editor.IsRemote() || handlerUI == nil || handlerUI.GetEditorComponent() == nil || !slices.Equal(m.editor.Render(40), []string{"factory-editor:"}) {
		t.Fatalf("editor remote=%v factory=%v rows=%q, want the factory's editor in the editor's place", m.editor.IsRemote(), handlerUI != nil && handlerUI.GetEditorComponent() != nil, m.editor.Render(40))
	}

	// session_shutdown-style teardown: every member disposes the component it built.
	ui.SetHeader(nil)
	ui.SetFooter(nil)
	ui.SetWidgetFactory("w", nil, nil)
	runPostedTasks(t, m)
	if header.disposed != 1 || footer.disposed != 1 || widget.disposed != 1 {
		t.Fatalf("disposed header/footer/widget = %d/%d/%d, want 1/1/1", header.disposed, footer.disposed, widget.disposed)
	}
}
