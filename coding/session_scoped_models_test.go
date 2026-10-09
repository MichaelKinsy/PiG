package coding

import (
	"context"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

func TestSessionScopedModelsReachRuntimeAndRunner(t *testing.T) {
	var observed []extension.ScopedModel
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{"session_start": {func(args ...any) (any, error) {
		var err error
		observed, err = extension.FromContext(args[1].(context.Context)).ScopedModels()
		return nil, err
	}}}}
	rt, err := NewRuntime(RuntimeOptions{Services: newTestServices(t), NewExtensions: []extension.Extension{ext}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	cold := rt.NewExtensionRunner().CreateCommandContext()
	models := []extension.ScopedModel{{Model: &ai.Model{ID: "scoped-test"}, ThinkingLevel: "high"}}
	session, err := rt.New(SessionStartOptions{Model: fakeModel(), ScopedModels: models, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if got := session.ScopedModels(); len(got) != 1 || &got[0] != &models[0] {
		t.Fatal("Session copied the supplied scope")
	}
	// Mode actions can be installed after Session construction without erasing Session-owned scope.
	rt.NewExtensionRunner().BindCore(extension.ExtensionActions{}, extension.ContextActions{}, nil)
	session.EmitSessionStart("startup")
	if len(observed) != 1 || &observed[0] != &models[0] {
		t.Fatal("event did not receive the Session scope")
	}
	if old, err := cold.ScopedModels(); err != nil || old == nil || len(old) != 0 {
		t.Fatalf("old unbound callback changed: %v %v", old, err)
	}
	models = []extension.ScopedModel{{Model: &ai.Model{ID: "replacement"}}}
	session.SetScopedModels(models)
	session.EmitSessionStart("reload")
	if len(observed) != 1 || &observed[0] != &models[0] {
		t.Fatal("replacement scope not visible")
	}
	fresh := inproc.NewRunner([]extension.Extension{ext}, session.CWD())
	session.ReplaceRunner(fresh)
	session.EmitSessionStart("reload")
	if len(observed) != 1 || &observed[0] != &models[0] {
		t.Fatal("replacement runner lost scope")
	}
	session.SetScopedModels(nil)
	session.EmitSessionStart("reload")
	if observed == nil || len(observed) != 0 {
		t.Fatalf("clear=%v", observed)
	}
}

// Pi: packages/coding-agent/src/core/agent-session.ts:478 (Session.scopedModels); packages/coding-agent/src/core/extensions/types.ts:344 (SessionOptions.scopedModels).
func TestCloneRetainsScopeWithoutStealingBinding(t *testing.T) {
	runner := inproc.NewRunner(nil, t.TempDir())
	models := []extension.ScopedModel{{Model: &ai.Model{ID: "source"}}}
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), ScopedModels: models, Runner: runner, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	// agent-session-runtime.ts:312-316 requires a saved source before cloning.
	appendAsst(t, session, "saved source")
	cloned, err := session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cloned.Close() })
	if got := cloned.ScopedModels(); len(got) != 1 || &got[0] != &models[0] {
		t.Fatal("clone copied or lost scope")
	}
	cloned.SetScopedModels([]extension.ScopedModel{{Model: &ai.Model{ID: "clone"}}})
	got, err := runner.CreateCommandContext().ScopedModels()
	if err != nil || len(got) != 1 || &got[0] != &models[0] {
		t.Fatalf("clone stole source binding: %v %v", got, err)
	}
}

func TestScopedModelsConcurrentPublication(t *testing.T) {
	session := &Session{}
	models := []extension.ScopedModel{{Model: &ai.Model{ID: "stable"}}}
	var work sync.WaitGroup
	for range 4 {
		work.Go(func() {
			for range 1000 {
				_ = session.ScopedModels()
			}
		})
	}
	for range 1000 {
		session.SetScopedModels(models)
		session.SetScopedModels(nil)
	}
	work.Wait()
}

func TestPersistedModelExtendsOnlyNonemptyScope(t *testing.T) {
	session, err := NewSession(newTestServices(t), SessionOptions{Model: fakeModel(), NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	first := fakeModel()
	first.ID = "first"
	second := fakeModel()
	second.ID = "second"
	scope := []extension.ScopedModel{{Model: first, ThinkingLevel: "high"}}
	session.SetScopedModels(scope)
	if err := session.SetModel(second); err != nil {
		t.Fatal(err)
	}
	if got := session.ScopedModels(); len(got) != 1 || &got[0] != &scope[0] {
		t.Fatal("non-persisted selection changed scope")
	}
	if err := session.SetModel(second, ModelMutationOptions{Persist: true}); err != nil {
		t.Fatal(err)
	}
	got := session.ScopedModels()
	if len(got) != 2 || got[1].Model.ID != "second" || got[1].ThinkingLevel != "" {
		t.Fatalf("persisted scope=%v", got)
	}
	if len(scope) != 1 {
		t.Fatal("append mutated old scope")
	}
	if err := session.SetModel(second, ModelMutationOptions{Persist: true}); err != nil {
		t.Fatal(err)
	}
	if len(session.ScopedModels()) != 2 {
		t.Fatal("duplicate model appended")
	}
	session.SetScopedModels(nil)
	if err := session.SetModel(first, ModelMutationOptions{Persist: true}); err != nil {
		t.Fatal(err)
	}
	if len(session.ScopedModels()) != 0 {
		t.Fatal("empty scope became restricted")
	}
}
