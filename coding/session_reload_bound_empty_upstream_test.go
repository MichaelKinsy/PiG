package coding

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// reloadProbeSession is a session whose extension records session_shutdown and session_start in events, with a resource loader that loads the same extension again.
func reloadProbeSession(t *testing.T) (*Session, *[]string) {
	t.Helper()
	root := t.TempDir()
	events := new([]string)
	record := func(args ...any) (any, error) {
		switch e := args[0].(type) {
		case extension.SessionShutdownEvent:
			*events = append(*events, e.Type+":"+e.Reason)
		case extension.SessionStartEvent:
			*events = append(*events, e.Type+":"+e.Reason)
		}
		return nil, nil
	}
	handlers := func() map[string][]extension.HandlerFn {
		return map[string][]extension.HandlerFn{"session_shutdown": {record}, "session_start": {record}}
	}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Extensions: []extension.Extension{{Path: "probe", Handlers: handlers()}}}, nil
		},
	})
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: inproc.NewRunner([]extension.Extension{{Path: "probe", Handlers: handlers()}}, root), ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, events
}

func requireReloadEvents(t *testing.T, session *Session, events *[]string, want ...string) {
	t.Helper()
	*events = nil
	if err := session.Reload(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*events, want) {
		t.Fatalf("events after reload = %v, want %v", *events, want)
	}
}

// Ports "emits session_start after reload when extensions were bound without bindings" of packages/coding-agent/test/agent-session-runtime-events.test.ts
// (Pi e03dca7a3843): the host binds the extensions with bindExtensions({}), the Go ExtensionBindings{} (no UI context, no command actions, no shutdown
// handler, no error listener), and a reload then emits session_shutdown to the old extensions and session_start with reason reload to the new ones.
// agent-session.ts bindExtensions sets _extensionsBound and reload() restarts the extensions on that flag, not on the bindings it holds.
func TestSessionReloadAfterBindExtensionsWithEmptyBindingsEmitsSessionStart(t *testing.T) {
	session, events := reloadProbeSession(t)
	if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"session_start:startup"}; !reflect.DeepEqual(*events, want) {
		t.Fatalf("events after the bind = %v, want %v", *events, want)
	}
	requireReloadEvents(t, session, events, "session_shutdown:reload", "session_start:reload")
}

// The other cases reload() distinguishes through _extensionsBound: a host that never called bindExtensions gets session_shutdown only (as before the
// commit); one that bound with members gets session_start; one that binds again after a reload (session_shutdown, session_start, bindExtensions) is bound still.
func TestSessionReloadRestartsTheExtensionsOnlyOfABoundSession(t *testing.T) {
	t.Run("never bound", func(t *testing.T) {
		session, events := reloadProbeSession(t)
		requireReloadEvents(t, session, events, "session_shutdown:reload")
	})
	t.Run("bound with members", func(t *testing.T) {
		session, events := reloadProbeSession(t)
		if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext, OnError: func(*extension.ExtensionError) {}}); err != nil {
			t.Fatal(err)
		}
		requireReloadEvents(t, session, events, "session_shutdown:reload", "session_start:reload")
	})
	t.Run("bound again after a reload", func(t *testing.T) {
		session, events := reloadProbeSession(t)
		if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
			t.Fatal(err)
		}
		requireReloadEvents(t, session, events, "session_shutdown:reload", "session_start:reload")
		if err := session.BindExtensions(t.Context(), ExtensionBindings{}); err != nil {
			t.Fatal(err)
		}
		requireReloadEvents(t, session, events, "session_shutdown:reload", "session_start:reload")
	})
}
