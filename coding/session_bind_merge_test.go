package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// agent-session.ts:3244-3265 bindExtensions(bindings): each member is assigned only when defined, so a later call that sets one member (the command actions) keeps the UI context
// an earlier call set; the bindings argument is required (an empty object binds nothing new).
func TestBindExtensionsKeepsMembersTheLaterCallLeavesUnset(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	type marker struct{ extension.UIContext }
	if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: marker{}, Mode: extension.ModeTUI}); err != nil {
		t.Fatal(err)
	}
	if runner.GetUIContext() == extension.NoopUIContext {
		t.Fatal("the UI context was not bound")
	}
	for name, later := range map[string]ExtensionBindings{
		"command actions only": {CommandContextActions: extension.CommandActions{WaitForIdle: func() error { return nil }}},
		"nothing":              {},
	} {
		if err := session.BindExtensions(t.Context(), later); err != nil {
			t.Fatal(err)
		}
		if runner.GetUIContext() == extension.NoopUIContext {
			t.Fatalf("a later bind with %s dropped the UI context", name)
		}
	}
	stored := session.extensionBindings.Load()
	if stored == nil || stored.UIContext == nil || stored.Mode != extension.ModeTUI || stored.CommandContextActions.WaitForIdle == nil {
		t.Fatalf("stored bindings = %+v, want the UI context, mode and command actions merged", stored)
	}
}
