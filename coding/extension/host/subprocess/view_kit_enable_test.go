package subprocess

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/extensions/sdk/frontend"
)

// The package's tests draw widgets and editors, as the pig command does, so they enable both once. A program that enables
// neither leaves every widget builder, tui.Editor and the dictionaries behind it out of its binary
// (TestHeadlessEmbedLinksNoViewWidget, TestHeadlessEmbedBinaryHoldsNoWordDictionary in examples/headless).
func init() {
	EnableViewWidgets()
	EnableEditorViews()
}

// withoutViewWidgetBuilders makes the test a program that enabled no builder yet. The table is the process's, so the test
// must not run in parallel with one that builds a view.
func withoutViewWidgetBuilders(t *testing.T) {
	t.Helper()
	enabled := viewWidgetBuilders.Swap(nil)
	t.Cleanup(func() { viewWidgetBuilders.Store(enabled) })
}

func enabledViewKinds() map[string]widgetBuildFunc {
	if enabled := viewWidgetBuilders.Load(); enabled != nil {
		return *enabled
	}
	return nil
}

const (
	notEnabledGrid   = "grid views are not enabled in this program: call subprocess.EnableViewWidgets before building a view that holds a grid"
	notEnabledEditor = "editor views are not enabled in this program: call subprocess.EnableEditorViews before building a view that holds an editor"
	gridOnlyView     = `{"root":{"kind":"grid","id":"g","columns":1,"cells":[{"value":"a","label":"1"}]}}`
	editorOnlyView   = `{"root":{"kind":"editor","id":"e"}}`
)

// Every registered kind fails to build until the call that enables it ran, with one sentence that names the kind and the
// call; the two calls fill one table in either order, the editor only by its own call, and a repeated call changes
// nothing.
func TestViewWidgetBuildNamesTheCallUntilItsBuilderIsEnabled(t *testing.T) {
	withoutViewWidgetBuilders(t)
	if len(viewWidgets) != 11 {
		t.Fatalf("%d widget kinds are registered, want the 11 of the component kit", len(viewWidgets))
	}
	build := func(kind string) error {
		_, err := (&viewBuilder{}).buildWidget(&ViewNode{Kind: kind, ID: "n"}, &frontend.ViewNode{})
		return err
	}
	for kind := range viewWidgets {
		call, article := "EnableViewWidgets", "a"
		switch kind {
		case ViewKindEditor:
			call, article = "EnableEditorViews", "an"
		case ViewKindInput:
			article = "an"
		}
		want := kind + " views are not enabled in this program: call subprocess." + call + " before building a view that holds " + article + " " + kind
		if err := build(kind); err == nil || err.Error() != want {
			t.Errorf("building %s before its builder is enabled: err = %v, want %q", kind, err, want)
		}
	}
	if err := build(ViewKindGrid); err == nil || err.Error() != notEnabledGrid {
		t.Errorf("grid: err = %v, want %q", err, notEnabledGrid)
	}
	if err := build(ViewKindEditor); err == nil || err.Error() != notEnabledEditor {
		t.Errorf("editor: err = %v, want %q", err, notEnabledEditor)
	}
	if err := build("widget"); err == nil || err.Error() != `unknown view kind "widget"` {
		t.Errorf("a kind in no registry: err = %v", err)
	}

	for _, order := range [][]func(){{EnableViewWidgets, EnableEditorViews}, {EnableEditorViews, EnableViewWidgets}} {
		viewWidgetBuilders.Store(nil)
		order[0]()
		first := enabledViewKinds()
		if _, editor := first[ViewKindEditor]; editor != (len(first) == 1) || len(first) != 1 && len(first) != 10 {
			t.Fatalf("one call enabled %d kinds, editor %v: want the editor alone or the ten others", len(first), editor)
		}
		if _, editor := first[ViewKindEditor]; !editor {
			if err := build(ViewKindEditor); err == nil || err.Error() != notEnabledEditor {
				t.Errorf("editor after EnableViewWidgets alone: err = %v, want %q", err, notEnabledEditor)
			}
		} else if err := build(ViewKindGrid); err == nil || err.Error() != notEnabledGrid {
			t.Errorf("grid after EnableEditorViews alone: err = %v, want %q", err, notEnabledGrid)
		}
		order[1]()
		table := viewWidgetBuilders.Load()
		order[0]()
		order[1]()
		if viewWidgetBuilders.Load() != table {
			t.Error("a repeated call stored a new table")
		}
		for kind := range viewWidgets {
			if enabledViewKinds()[kind] == nil {
				t.Errorf("%s has no builder after both calls", kind)
			}
		}
		if len(enabledViewKinds()) != len(viewWidgets) {
			t.Errorf("%d builders for %d registered kinds", len(enabledViewKinds()), len(viewWidgets))
		}
	}
}

// The calls may run while views are built: a build sees the table before or after a call, never a table being written.
func TestEnablingViewWidgetsIsSafeDuringABuild(t *testing.T) {
	withoutViewWidgetBuilders(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(EnableViewWidgets)
		wg.Go(EnableEditorViews)
		wg.Go(func() {
			for range 50 {
				for _, view := range []string{gridOnlyView, editorOnlyView} {
					s := newViewSurface("", newViewImageStore())
					if err := s.accept(json.RawMessage(view), nil); err != nil && err.Error() != notEnabledGrid && err.Error() != notEnabledEditor {
						t.Errorf("building %s while the builders are enabled: %v", view, err)
					}
					s.close()
				}
			}
		})
	}
	wg.Wait()
	for _, view := range []string{gridOnlyView, editorOnlyView} {
		s := newViewSurface("", newViewImageStore())
		if err := s.accept(json.RawMessage(view), nil); err != nil {
			t.Errorf("building %s after both calls: %v", view, err)
		}
		s.close()
	}
}

// A program that did not enable a kind's builder rejects the view that holds it as it rejects any view it cannot build:
// a renderer's result is a failed render, the rejection is reported once per surface through the runner's error
// listeners with the sentence that names the kind and the call, and the extension is sent nothing. The answer is the same
// for a widget and for the editor.
func TestViewOfAKindThatIsNotEnabledIsReportedOnceAndTheExtensionIsSentNothing(t *testing.T) {
	withoutViewWidgetBuilders(t)
	for _, tc := range []struct{ name, view, want string }{
		{"grid", gridOnlyView, notEnabledGrid},
		{"editor", editorOnlyView, notEnabledEditor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(t.TempDir())
			conn, peer := newPipeConn()
			sent := make(chan int64, 1)
			go func() {
				n, _ := io.Copy(io.Discard, peer)
				sent <- n
			}()
			h.observeXref(conn)
			h.mu.Lock()
			h.exts["peer"] = &managedExt{config: ExtConfig{Name: "peer", Source: "/ext/peer.go"}}
			h.mu.Unlock()
			var reported []*extension.ExtensionError
			runner := inproc.NewRunner(nil, t.TempDir(), h.Runtime())
			runner.AddErrorListener(func(err *extension.ExtensionError) { reported = append(reported, err) })
			runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)

			var view rendererView
			for range 2 {
				if lines, ok := view.apply(conn, func() {}, "", nil, RenderResult{View: json.RawMessage(tc.view)}, 80); ok || lines != nil {
					t.Fatalf("a view of a kind that is not enabled rendered %q, ok %v: want a failed render", lines, ok)
				}
			}
			want := "extension view rejected on renderer (the surface keeps its previous frame): " + tc.want
			if len(reported) != 1 || reported[0].Event != "view" || reported[0].ExtensionPath != "/ext/peer.go" || reported[0].Error != want {
				var got []string
				for _, r := range reported {
					got = append(got, r.Event+"|"+r.ExtensionPath+"|"+r.Error)
				}
				t.Fatalf("reported %d times: %s\nwant once: %q", len(reported), strings.Join(got, "\n"), want)
			}
			_ = peer.Close()
			if n := <-sent; n != 0 {
				t.Fatalf("the extension was sent %d bytes for the rejected view, want nothing", n)
			}
		})
	}
}
