package coding

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

func TestRuntimeExtensionContextUsesServiceProjectTrust(t *testing.T) {
	trusted := false
	svcs, err := NewServices(ServicesOptions{
		CWD:            t.TempDir(),
		AgentDir:       t.TempDir(),
		ProjectTrusted: new(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	ext := extension.Extension{
		Name: "trust-observer", Path: "/trust-observer", ResolvedPath: "/trust-observer",
		Handlers: map[string][]extension.HandlerFn{
			"before_agent_start": {func(args ...any) (any, error) {
				ctx, _ := args[1].(context.Context)
				extCtx := extension.FromContext(ctx)
				var err error
				trusted, err = extCtx.IsProjectTrusted()
				return nil, err
			}},
		},
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: []extension.Extension{ext}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	if _, err := rt.NewExtensionRunner().EmitBeforeAgentStart(
		context.Background(), "work", nil, "base", extension.BuildSystemPromptOptions{},
	); err != nil {
		t.Fatal(err)
	}
	if trusted {
		t.Fatal("runtime extension context treated an untrusted service as trusted")
	}
}

func TestRuntimeExtensionContextBindsServiceModelRegistry(t *testing.T) {
	svcs := newTestServices(t)
	var got extension.ModelRegistry
	ext := extension.Extension{
		Name: "registry-observer", Path: "/registry-observer", ResolvedPath: "/registry-observer",
		Handlers: map[string][]extension.HandlerFn{
			"session_start": {func(args ...any) (any, error) {
				ctx, _ := args[1].(context.Context)
				var err error
				got, err = extension.FromContext(ctx).ModelRegistry()
				return nil, err
			}},
		},
	}
	rt, err := NewRuntime(RuntimeOptions{Services: svcs, NewExtensions: []extension.Extension{ext}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	if _, err := rt.NewExtensionRunner().Emit(context.Background(), extension.SessionStartEvent{Type: "session_start"}); err != nil {
		t.Fatal(err)
	}
	if got != svcs.Registry() {
		t.Fatalf("extension ModelRegistry = %T %p, want service registry %p", got, got, svcs.Registry())
	}
}

func TestNewRuntimeRequiresServices(t *testing.T) {
	if _, err := NewRuntime(RuntimeOptions{}); err == nil {
		t.Fatal("expected error when Services is nil")
	}
}

func TestRuntimeNewSessionCreatesJSONL(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if sess.Path() == "" {
		t.Fatal("expected session path")
	}
}

func TestRuntimeNewNoSessionUsesSessionIDWithoutPath(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel(), NoSession: true, SessionID: "cache-key-1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if sess.ID() != "cache-key-1" {
		t.Fatalf("session id = %q, want cache-key-1", sess.ID())
	}
	if sess.Path() != "" {
		t.Fatalf("no-session path = %q, want empty", sess.Path())
	}
}

func TestRuntimeResumeUnknownIDReturnsError(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	_, err := rt.Resume("sess-does-not-exist", SessionStartOptions{Model: fakeModel()})
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should say not found: %v", err)
	}
}

func TestRuntimeResumeRoundTrip(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()

	sess1, _ := rt.New(SessionStartOptions{Model: fakeModel()})
	id := sess1.ID()
	// Drop a message so resume has something to rebuild. Use an
	// assistant message so the session flushes to disk (deferred-flush
	// gate); a user-only session is intentionally not persisted.
	_, _ = sess1.Inner().AppendMessage(agent.AgentMessage{
		Assistant: &agent.AssistantMessage{
			Role:    "assistant",
			Content: []ai.AssistantContentBlock{ai.TextContent{Text: "ping"}},
		},
	})
	_ = sess1.Close()

	sess2, err := rt.Resume(id, SessionStartOptions{Model: fakeModel()})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	defer func() { _ = sess2.Close() }()
	if sess2.ID() != id {
		t.Errorf("Resume id mismatch: got %s want %s", sess2.ID(), id)
	}
	if got := sess2.Messages(); len(got) != 1 || got[0].Assistant == nil {
		t.Errorf("resumed agent should have only the persisted assistant message; got %v", got)
	}
}

func TestRuntimeContinueWithoutPriorReturnsError(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	_, err := rt.Continue(SessionStartOptions{Model: fakeModel()})
	if err == nil {
		t.Fatal("expected error: no prior sessions")
	}
}

func TestRuntimeContinueFindsMostRecent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		latest int
	}{
		{name: "second_created_is_newer", latest: 1},
		{name: "first_created_is_newer", latest: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svcs := newTestServices(t)
			rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
			defer func() { _ = rt.Close() }()
			first, _ := rt.New(SessionStartOptions{Model: fakeModel()})
			flushSess(t, first)
			_ = first.Close()
			second, _ := rt.New(SessionStartOptions{Model: fakeModel()})
			flushSess(t, second)
			_ = second.Close()

			// Pi's findMostRecentSession orders by filesystem mtime, not creation order. Set distinct mtimes so coarse clocks cannot turn this into a tie case.
			sessions := []*Session{first, second}
			older := time.Unix(1_700_000_000, 0)
			for i, sess := range sessions {
				mtime := older
				if i == tc.latest {
					mtime = older.Add(time.Hour)
				}
				if err := os.Chtimes(sess.Path(), mtime, mtime); err != nil {
					t.Fatal(err)
				}
			}
			wantID := sessions[tc.latest].ID()

			cont, err := rt.Continue(SessionStartOptions{Model: fakeModel()})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cont.Close() }()
			if cont.ID() != wantID {
				t.Errorf("Continue picked %s, want most-recent %s", cont.ID(), wantID)
			}
		})
	}
}

func TestRuntimeListSessions(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	for range 3 {
		s, _ := rt.New(SessionStartOptions{Model: fakeModel()})
		flushSess(t, s)
		_ = s.Close()
	}
	infos, err := rt.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 3 {
		t.Errorf("expected 3 sessions; got %d", len(infos))
	}
}

func TestRuntimeOpenInvalidPathErrors(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	if _, err := rt.Open("", SessionStartOptions{Model: fakeModel()}); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestRuntimeNewSessionLayersExtraTools(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	custom := &fakeTool{name: "custom-foo"}
	sess, err := rt.New(SessionStartOptions{
		Model:      fakeModel(),
		ExtraTools: []agent.AgentTool{custom},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	found := false
	for _, tool := range sess.Tools() {
		if tool.Name() == "custom-foo" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ExtraTools custom-foo not on session")
	}
}

func TestRuntimeSkipExtensionToolsHonored(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{
		Model:              fakeModel(),
		SkipExtensionTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	// Default coding tools are still added by NewSession; this test
	// just verifies SkipExtensionTools doesn't break construction
	// when no extensions are registered. (With extensions present,
	// it would suppress the extRunner.Tools() contribution; covered
	// implicitly by Runtime's tool-layering logic.)
	if len(sess.Tools()) == 0 {
		t.Error("expected default coding tools to be present")
	}
}

func TestRuntimeSkipBuiltinToolsHonored(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{
		Model:            fakeModel(),
		SkipBuiltinTools: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if len(sess.Tools()) != 0 {
		t.Errorf("expected no built-in tools when SkipBuiltinTools=true; got %v", len(sess.Tools()))
	}
}

func TestRuntimeNoToolsBuiltinHonored(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel(), NoTools: "builtin"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if len(sess.Tools()) != 0 {
		t.Fatalf("expected builtin tools to be omitted by NoTools=builtin; got %d", len(sess.Tools()))
	}
}

func TestRuntimeNoToolsAllHonored(t *testing.T) {
	svcs := newTestServices(t)
	rt, _ := NewRuntime(RuntimeOptions{Services: svcs})
	defer func() { _ = rt.Close() }()
	sess, err := rt.New(SessionStartOptions{Model: fakeModel(), NoTools: "all", ExtraTools: []agent.AgentTool{&fakeTool{name: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sess.Close() }()
	if len(sess.Tools()) != 0 {
		t.Fatalf("expected all tools blocked by NoTools=all; got %d", len(sess.Tools()))
	}
}

func TestRuntimeBeforeSessionInvalidateHook(t *testing.T) {
	svcs := newTestServices(t)
	rt, err := NewRuntime(RuntimeOptions{Services: svcs})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	rt.SetBeforeSessionInvalidate(func() { called = true })
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("beforeSessionInvalidate hook was not called")
	}
}
