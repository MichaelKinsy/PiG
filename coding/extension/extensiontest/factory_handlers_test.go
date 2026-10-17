package extensiontest_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/factoryload"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// types.ts:698 ProjectTrustHandler and :1558 on("project_trust", handler); runner.ts:295 emitProjectTrustEvent: a handler an extension factory registers through
// the loader's API is called by the runner with the event, the extension context's ProjectTrustContext, and the first "yes"/"no" it returns decides;
// "undecided" falls through to the next handler.
// mutation-checked: OnProjectTrust dropping the trust context argument, or the loader not keeping the handler, fails.
func TestFactoryRegisteredProjectTrustHandlersDecideInRegistrationOrder(t *testing.T) {
	var calls []string
	var seenTrust []extension.ProjectTrustContext
	handler := func(name string, decision extension.ProjectTrustEventDecision) extension.ProjectTrustHandler {
		return func(_ context.Context, event extension.ProjectTrustEvent, trust extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
			calls = append(calls, name+":"+event.Cwd)
			seenTrust = append(seenTrust, trust)
			return extension.ProjectTrustEventResult{Trusted: decision}, nil
		}
	}
	loaded, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
		api.OnProjectTrust(handler("first", extension.ProjectTrustUndecided))
		api.OnProjectTrust(handler("second", extension.ProjectTrustNo))
		api.OnProjectTrust(handler("third", extension.ProjectTrustYes))
		return nil
	}, "/project", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<trust>")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{loaded}, t.TempDir())
	want := extension.ProjectTrustContext{Cwd: "/project", Mode: extension.ModeRPC, HasUI: true, UI: extension.NoopUIContext}
	result, _, err := inproc.EmitProjectTrust(runner, t.Context(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"}, want)
	if err != nil || result == nil || result.Trusted != extension.ProjectTrustNo {
		t.Fatalf("decision = %+v, %v, want the second handler's \"no\"", result, err)
	}
	if len(calls) != 2 || calls[0] != "first:/project" || calls[1] != "second:/project" {
		t.Fatalf("handlers called %v, want first then second, stopping at the decision", calls)
	}
	if seenTrust[0] != want || seenTrust[1] != want {
		t.Fatalf("handlers saw trust contexts %+v, want %+v", seenTrust, want)
	}
}

// types.ts EntryRenderer and runner.ts getEntryRenderer: a renderer an extension factory registers for a custom entry type is the one the runner returns for
// that type, called with the entry, the render options and the theme; another type has none.
// mutation-checked: RegisterEntryRenderer not keeping the renderer under its type, or the runner keying it by another type, fails.
func TestFactoryRegisteredEntryRendererIsTheRunnersRendererForItsType(t *testing.T) {
	var gotData any
	var gotExpanded bool
	renderer := extension.EntryRenderer(func(entry extension.CustomEntry, options extension.EntryRenderOptions, _ extension.Theme) extension.Component {
		gotData, gotExpanded = entry.Data, options.Expanded
		return nil
	})
	loaded, err := factoryload.LoadExtensionFromFactory(func(api extension.API) error {
		api.RegisterEntryRenderer("note", renderer)
		return nil
	}, "/project", extension.CreateEventBus(), extension.CreateExtensionRuntime(), "<entry>")
	if err != nil {
		t.Fatal(err)
	}
	runner := inproc.NewRunner([]extension.Extension{loaded}, t.TempDir())
	if runner.EntryRenderer("other") != nil {
		t.Fatal("a type with no registered renderer got one")
	}
	got := runner.EntryRenderer("note")
	if got == nil {
		t.Fatal("the registered renderer is missing")
	}
	got(extension.CustomEntry{CustomType: "note", Data: "payload"}, extension.EntryRenderOptions{Expanded: true}, nil)
	if gotData != "payload" || !gotExpanded {
		t.Fatalf("renderer saw data %v expanded %v, want payload true", gotData, gotExpanded)
	}
}
