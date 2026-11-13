package coding

// pi: packages/coding-agent/src/core/agent-session.ts

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type reloadOrder struct {
	mu    sync.Mutex
	steps []string
}

func (o *reloadOrder) add(step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = append(o.steps, step)
}

func (o *reloadOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.steps...)
}

// newReloadOrderSession is a bound Session whose reloaded extension records session_start and resources_discover. startHook
// runs inside the session_start handler.
func newReloadOrderSession(t *testing.T, order *reloadOrder, startHook func()) *Session {
	t.Helper()
	root := t.TempDir()
	ext := extension.Extension{Path: "new", Handlers: map[string][]extension.HandlerFn{
		"session_start": {func(args ...any) (any, error) {
			order.add("session_start:" + args[0].(extension.SessionStartEvent).Reason)
			if startHook != nil {
				startHook()
			}
			return nil, nil
		}},
		"resources_discover": {func(args ...any) (any, error) {
			order.add("resources_discover:" + args[0].(extension.ResourcesDiscoverEvent).Reason)
			return nil, nil
		}},
	}}
	loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: root, AgentDir: filepath.Join(root, "agent"), NoSkills: true, NoPromptTemplates: true, NoThemes: true, NoContextFiles: true,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Extensions: []extension.Extension{ext}}, nil
		},
	})
	if err := loader.Reload(); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(newTestServices(t), SessionOptions{NoSession: true, Runner: inproc.NewRunner([]extension.Extension{ext}, root), ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// agent-session.ts:3815-3850: reload awaits beforeSessionStart, then session_start, then the resources_discover round, in that
// order, and returns only after the last; a beforeSessionStart failure rejects the reload before session_start is emitted.
func TestSessionReloadAwaitsBeforeSessionStartThenSessionStartThenResourceDiscovery(t *testing.T) {
	t.Run("awaits each step in order", func(t *testing.T) {
		var order reloadOrder
		startRan := make(chan struct{}, 1)
		session := newReloadOrderSession(t, &order, func() { startRan <- struct{}{} })
		if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext}); err != nil {
			t.Fatal(err)
		}
		<-startRan
		order.reset()
		entered, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			done <- session.Reload(t.Context(), WithBeforeSessionStart(func(context.Context) error {
				order.add("beforeSessionStart")
				close(entered)
				<-release
				order.add("beforeSessionStart:done")
				return nil
			}))
		}()
		<-entered
		select {
		case <-startRan:
			t.Fatal("session_start ran while beforeSessionStart was still pending")
		case err := <-done:
			t.Fatalf("Reload returned (%v) while beforeSessionStart was still pending", err)
		case <-time.After(100 * time.Millisecond):
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		want := []string{"beforeSessionStart", "beforeSessionStart:done", "session_start:reload", "resources_discover:reload"}
		if got := order.snapshot(); !equalStrings(got, want) {
			t.Fatalf("order = %v, want %v", got, want)
		}
	})
	t.Run("a beforeSessionStart failure rejects the reload before session_start", func(t *testing.T) {
		var order reloadOrder
		startRan := make(chan struct{}, 4)
		session := newReloadOrderSession(t, &order, func() { startRan <- struct{}{} })
		if err := session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext}); err != nil {
			t.Fatal(err)
		}
		<-startRan
		order.reset()
		boom := errors.New("before start failed")
		err := session.Reload(t.Context(), WithBeforeSessionStart(func(context.Context) error { return boom }))
		if !errors.Is(err, boom) {
			t.Fatalf("Reload error = %v, want %v", err, boom)
		}
		if got := order.snapshot(); len(got) != 0 {
			t.Fatalf("steps after the failed beforeSessionStart = %v, want none", got)
		}
	})
}

// agent-session.ts:3413-3436: bindExtensions awaits session_start (a suspended handler holds it) before the resources_discover
// round, and returns only after the round.
func TestSessionBindExtensionsAwaitsSessionStartBeforeResourceDiscovery(t *testing.T) {
	var order reloadOrder
	entered, release := make(chan struct{}), make(chan struct{})
	session := newReloadOrderSession(t, &order, func() {
		close(entered)
		<-release
		order.add("session_start:done")
	})
	done := make(chan error, 1)
	go func() {
		done <- session.BindExtensions(t.Context(), ExtensionBindings{UIContext: extension.NoopUIContext})
	}()
	<-entered
	select {
	case err := <-done:
		t.Fatalf("BindExtensions returned (%v) while the session_start handler was suspended", err)
	case <-time.After(100 * time.Millisecond):
	}
	if got := order.snapshot(); !equalStrings(got, []string{"session_start:startup"}) {
		t.Fatalf("steps while session_start is suspended = %v: resources_discover must wait for it", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	want := []string{"session_start:startup", "session_start:done", "resources_discover:startup"}
	if got := order.snapshot(); !equalStrings(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func (o *reloadOrder) reset() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = nil
}
