package coding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func bindingsSession(t *testing.T, root string, starts *[]string) *Session {
	t.Helper()
	runner := inproc.NewRunner([]extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
		"session_start": {func(args ...any) (any, error) {
			*starts = append(*starts, args[0].(extension.SessionStartEvent).Reason)
			return nil, nil
		}},
	}}}, root)
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Extensions: []extension.Extension{{Path: "ext", Handlers: map[string][]extension.HandlerFn{
				"session_start": {func(args ...any) (any, error) {
					*starts = append(*starts, args[0].(extension.SessionStartEvent).Reason)
					return nil, nil
				}},
			}}}}, nil
		},
	})
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: runner, ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// upstream: agent-session.ts:3259-3276 bindExtensions stores each defined field only: a later binding that omits a field keeps the earlier value.
func TestBindExtensionsKeepsFieldsALaterBindingOmits(t *testing.T) {
	var starts []string
	session := bindingsSession(t, t.TempDir(), &starts)
	if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext, Mode: extension.ModeRPC}); err != nil {
		t.Fatal(err)
	}
	called := ""
	if err := session.BindExtensions(t.Context(), ExtensionBindings{ShutdownHandler: func() { called = "shutdown" }}); err != nil {
		t.Fatal(err)
	}
	stored := *session.extensionBindings.Load()
	if stored.UIContext == nil || stored.Mode != extension.ModeRPC {
		t.Fatalf("a binding without UIContext and Mode dropped them: %+v", stored)
	}
	session.extensionShutdown()()
	if called != "shutdown" {
		t.Fatalf("ctx.shutdown did not reach the bound handler, called = %q", called)
	}
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if got := session.extensionBindings.Load(); got.UIContext == nil || got.ShutdownHandler == nil {
		t.Fatalf("an empty binding replaced the stored bindings: %+v", got)
	}
}

// upstream: agent-session.ts:3442-3447 abort() calls the bound abort handler instead of aborting the session.
func TestBindExtensionsAbortHandlerReplacesTheSessionAbort(t *testing.T) {
	var starts []string
	session := bindingsSession(t, t.TempDir(), &starts)
	aborted := 0
	if err := session.BindExtensions(t.Context(), ExtensionBindings{AbortHandler: func() { aborted++ }}); err != nil {
		t.Fatal(err)
	}
	session.extensionAbort()
	if aborted != 1 {
		t.Fatalf("the abort handler ran %d times, want 1", aborted)
	}
}

// upstream: agent-session.ts:3336-3343 the error listener of the latest binding receives extension errors and replaces the earlier one.
func TestBindExtensionsOnErrorReplacesTheEarlierListener(t *testing.T) {
	var starts []string
	session := bindingsSession(t, t.TempDir(), &starts)
	var first, second []string
	if err := session.BindExtensions(t.Context(), ExtensionBindings{OnError: func(e *extension.ExtensionError) { first = append(first, e.Error) }}); err != nil {
		t.Fatal(err)
	}
	if err := session.BindExtensions(t.Context(), ExtensionBindings{OnError: func(e *extension.ExtensionError) { second = append(second, e.Error) }}); err != nil {
		t.Fatal(err)
	}
	session.ExtensionRunner().EmitError(&extension.ExtensionError{Error: errors.New("boom").Error()})
	if len(first) != 0 || len(second) != 1 {
		t.Fatalf("listeners saw first=%v second=%v, want only the later one", first, second)
	}
}

// upstream: agent-session.ts:3688-3698 reload replays session_start only when a UI context, command actions, a shutdown handler or an error listener is bound (agent-session-runtime tests bind `{ shutdownHandler: () => {} }`); a mode alone or an abort handler alone is not a binding.
func TestReloadReplaysSessionStartOnlyForRealBindings(t *testing.T) {
	cases := []struct {
		name     string
		bindings ExtensionBindings
		want     int
	}{
		{"shutdown handler", ExtensionBindings{ShutdownHandler: func() {}}, 1},
		{"error listener", ExtensionBindings{OnError: func(*extension.ExtensionError) {}}, 1},
		{"mode only", ExtensionBindings{Mode: extension.ModeRPC}, 0},
		{"abort handler only", ExtensionBindings{AbortHandler: func() {}}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var starts []string
			session := bindingsSession(t, t.TempDir(), &starts)
			if err := session.BindExtensions(t.Context(), tc.bindings); err != nil {
				t.Fatal(err)
			}
			starts = nil
			if err := session.Reload(t.Context()); err != nil {
				t.Fatal(err)
			}
			if len(starts) != tc.want {
				t.Fatalf("reload emitted session_start %d times (%v), want %d", len(starts), starts, tc.want)
			}
		})
	}
}
