package coding

import (
	"path/filepath"
	"reflect"
	"testing"

	"context"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Ports packages/coding-agent/src/core/agent-session.ts:3640-3685 reload(): the old extensions get session_shutdown with reason reload and go
// stale, the resource loader reloads and its extensions build the new runner (agent-session.ts:3616 getExtensions) with the previous flag
// values, and, because the mode bound its UI/command actions, the new runner receives session_start with reason reload.
func TestSessionReloadRebuildsTheRunnerFromTheResourceLoader(t *testing.T) {
	root := t.TempDir()
	var order []string
	handler := func(label string) extension.HandlerFn {
		return func(args ...any) (any, error) {
			switch e := args[0].(type) {
			case extension.SessionShutdownEvent:
				order = append(order, label+":shutdown:"+e.Reason)
			case extension.SessionStartEvent:
				order = append(order, label+":start:"+e.Reason)
			}
			return nil, nil
		}
	}
	oldRunner := inproc.NewRunner([]extension.Extension{{Path: "old", Handlers: map[string][]extension.HandlerFn{
		"session_shutdown": {handler("old")}, "session_start": {handler("old")},
	}}}, root)
	oldRunner.SetFlagValue("verbose", true)
	loads := 0
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			loads++
			return LoadExtensionsResult{Extensions: []extension.Extension{{Path: "new", Handlers: map[string][]extension.HandlerFn{
				"session_shutdown": {handler("new")}, "session_start": {handler("new")},
			}}}}, nil
		},
	})
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: oldRunner, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext}); err != nil {
		t.Fatal(err)
	}
	order = nil
	if err := session.Reload(t.Context(), WithBeforeSessionStart(func(context.Context) error {
		order = append(order, "beforeSessionStart")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if want := []string{"old:shutdown:reload", "beforeSessionStart", "new:start:reload"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("event order = %v, want %v", order, want)
	}
	if loads != 1 {
		t.Fatalf("the loader loaded extensions %d times, want once for the reload", loads)
	}
	rebuilt := session.ExtensionRunner()
	if rebuilt == oldRunner || !oldRunner.IsStale() || rebuilt.IsStale() {
		t.Fatalf("the old runner must go stale and a new one replace it (same=%v oldStale=%v newStale=%v)", rebuilt == oldRunner, oldRunner.IsStale(), rebuilt.IsStale())
	}
	if got := rebuilt.GetFlagValues()["verbose"]; got != true {
		t.Fatalf("the previous flag values must carry over, got %v", got)
	}

	// Without mode bindings Pi's reload rebuilds the runner but emits no session_start (hasBindings is false).
	unbound, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: inproc.NewRunner(nil, root), ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unbound.Close() })
	order = nil
	if err := unbound.Reload(t.Context(), WithBeforeSessionStart(func(context.Context) error {
		order = append(order, "beforeSessionStart")
		return nil
	})); err != nil {
		t.Fatal(err)
	}
	if len(order) != 0 {
		t.Fatalf("an unbound session emitted %v on reload", order)
	}
}

// Ports agent-session.ts:3654-3657: reload resets the API provider registry (resetApiProviders, compat.ts:209-213) after the settings reload and
// before the resource loader loads the extensions again. A provider registered before the reload is gone, a provider registered while the
// extensions load is kept, and the built-in providers stay registered.
func TestSessionReloadResetsAPIProvidersBeforeTheExtensionsLoad(t *testing.T) {
	const stale, reloaded ai.API = "test-reload-stale-api", "test-reload-loaded-api"
	t.Cleanup(ai.ResetAPIProviders)
	root := t.TempDir()
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			ai.RegisterAPIProvider(ai.APIProvider{API: reloaded}, "ext:loaded")
			return LoadExtensionsResult{}, nil
		},
	})
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	ai.RegisterAPIProvider(ai.APIProvider{API: stale}, "ext:removed")
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if ai.GetAPIProvider(stale) != nil {
		t.Fatalf("%s survived reload; resetApiProviders clears providers registered before it", stale)
	}
	if ai.GetAPIProvider(reloaded) == nil {
		t.Fatalf("%s registered while the extensions loaded is gone; the reset must run before loader.reload", reloaded)
	}
	if ai.GetAPIProvider(ai.APIAnthropicMessages) == nil {
		t.Fatal("the built-in providers are not registered again after the reset")
	}
}
