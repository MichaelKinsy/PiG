package coding

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

type reloadContextKey struct{}

// contextReloadLoader records the context its ReloadWith ran under.
type contextReloadLoader struct {
	ResourceLoader
	got context.Context
}

func (l *contextReloadLoader) ReloadWith(ctx context.Context, _ ...ResourceLoaderReloadOptions) error {
	l.got = ctx
	return nil
}

// Session.Reload reloads a loader that can take a context under the context of the reload call, so the caller's request values reach the resource reload (agent-session.ts reload awaits resourceLoader.reload for its caller).
func TestSessionReloadPassesItsContextToTheResourceLoader(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	loader := &contextReloadLoader{ResourceLoader: NoResources}
	session, err := NewSession(services, SessionOptions{ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	ctx := context.WithValue(context.Background(), reloadContextKey{}, "caller")
	if err := session.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if loader.got == nil || loader.got.Value(reloadContextKey{}) != "caller" {
		t.Fatalf("ReloadWith ran under %v, want the reload call's context", loader.got)
	}
}

// runtimeReloadLoader reports, after each reload, the extension runtime its extensions registered against, or fails the reload.
type runtimeReloadLoader struct {
	ResourceLoader
	runtime *extension.ExtensionRuntime
	err     error
}

func (l *runtimeReloadLoader) Reload(...ResourceLoaderReloadOptions) error { return l.err }

func (l *runtimeReloadLoader) GetExtensions() LoadExtensionsResult {
	return LoadExtensionsResult{Runtime: l.runtime}
}

// Pi's reload marks the old runner and its runtime stale right after session_shutdown (agent-session.ts:3663-3664, runner.ts:722-728), and the resource loader then loads the extensions against a fresh runtime (loader.ts:689). A Pig loader can hand the rebuilt runner the runtime the old runner bound (the CLI build's host runtime): that runtime must stay active, or every built-in extension of the reloaded Session reads a stale ctx. A runtime the rebuilt runner does not bind goes stale, and so does the old runtime when the reload fails.
// mutation-checked: invalidating the old runtime even when the rebuilt runner binds it fails the "kept" case; dropping the deferred invalidation fails the "replaced" and "failed" cases.
func TestSessionReloadInvalidatesTheOldRuntimeUnlessTheRebuiltRunnerKeepsIt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fresh     bool
		err       error
		wantStale bool
	}{
		{name: "kept"},
		{name: "replaced", fresh: true, wantStale: true},
		{name: "failed", err: errors.New("resource reload failed"), wantStale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			old := extension.CreateExtensionRuntime()
			loader := &runtimeReloadLoader{ResourceLoader: NoResources, runtime: old}
			session, err := NewSession(services, SessionOptions{ResourceLoader: loader})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = session.Close() }()
			if _, err := session.ReloadExtensions(nil, old); err != nil {
				t.Fatal(err)
			}
			loader.err = tc.err
			if tc.fresh {
				loader.runtime = extension.CreateExtensionRuntime()
			}
			if err := session.Reload(t.Context()); !errors.Is(err, tc.err) {
				t.Fatalf("Reload error = %v, want %v", err, tc.err)
			}
			if stale := old.AssertActive() != nil; stale != tc.wantStale {
				t.Fatalf("old runtime stale = %v, want %v", stale, tc.wantStale)
			}
			if tc.err == nil && session.ExtensionRunner().Runtime().AssertActive() != nil {
				t.Fatal("the reloaded runner's runtime is stale")
			}
		})
	}
}
