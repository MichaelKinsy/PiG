package extensiontest_test

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
)

func TestFake_ImplementsAPI(t *testing.T) {
	// Compile-time enforcement is in fake.go (`var _ extension.API = ...`).
	// This test is a runtime smoke: instantiate, confirm methods are
	// callable through the interface.
	var api extension.API = extensiontest.NewFake()
	api.SetSessionName("smoke")
	if got := api.GetSessionName(); got != "smoke" {
		t.Fatalf("session name round-trip: want smoke, got %q", got)
	}
}

func TestFake_RecordsRegisterTool(t *testing.T) {
	f := extensiontest.NewFake()
	def := extension.ToolDefinition{Name: "ping", Description: "pong"}
	f.RegisterTool(def)
	if len(f.RegisterToolCalls) != 1 {
		t.Fatalf("RegisterToolCalls: want 1, got %d", len(f.RegisterToolCalls))
	}
	if f.RegisterToolCalls[0].Name != "ping" {
		t.Errorf("recorded tool name: want ping, got %q", f.RegisterToolCalls[0].Name)
	}
}

func TestFake_RecordsOnHandlers(t *testing.T) {
	f := extensiontest.NewFake()
	called := false
	f.OnSessionStart(func(ctx context.Context, evt extension.SessionStartEvent) error {
		called = true
		return nil
	})
	if len(f.OnSessionStartHandlers) != 1 {
		t.Fatalf("OnSessionStartHandlers: want 1, got %d", len(f.OnSessionStartHandlers))
	}
	// Invoke the recorded handler manually to confirm it's the same fn.
	if err := f.OnSessionStartHandlers[0](context.Background(), extension.SessionStartEvent{}); err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if !called {
		t.Error("recorded handler was not the one passed in")
	}
}

func TestMemBus_PubSub(t *testing.T) {
	bus := extensiontest.NewMemBus()
	got := []string{}
	cancel1 := bus.On("topic", func(p any) {
		got = append(got, "h1:"+p.(string))
	})
	cancel2 := bus.On("topic", func(p any) {
		got = append(got, "h2:"+p.(string))
	})
	defer cancel2()

	bus.Emit("topic", "hello")
	if len(got) != 2 || got[0] != "h1:hello" || got[1] != "h2:hello" {
		t.Fatalf("after publish: got %v", got)
	}

	cancel1()
	bus.Emit("topic", "world")
	if len(got) != 3 || got[2] != "h2:world" {
		t.Fatalf("after cancel: got %v", got)
	}
}

// packages/coding-agent/src/core/extensions/types.ts:698 ProjectTrustHandler and :1558 on("project_trust", handler): a handler registered
// through the API keeps its registration order and is called with the event and the ProjectTrustContext of the decision.
// Pi: packages/coding-agent/src/core/extensions/types.ts:1558 (API.on).
func TestFake_RecordsProjectTrustHandlersWithTheirTrustContext(t *testing.T) {
	f := extensiontest.NewFake()
	var seen []extension.ProjectTrustContext
	register := func(decision extension.ProjectTrustEventDecision) {
		var handler extension.ProjectTrustHandler = func(_ context.Context, evt extension.ProjectTrustEvent, trust extension.ProjectTrustContext) (extension.ProjectTrustEventResult, error) {
			if evt.Cwd != "/project" {
				t.Errorf("event cwd = %q", evt.Cwd)
			}
			seen = append(seen, trust)
			return extension.ProjectTrustEventResult{Trusted: decision}, nil
		}
		var api extension.API = f
		api.OnProjectTrust(handler)
	}
	register(extension.ProjectTrustUndecided)
	register(extension.ProjectTrustYes)
	if len(f.OnProjectTrustHandlers) != 2 {
		t.Fatalf("OnProjectTrustHandlers = %d, want 2", len(f.OnProjectTrustHandlers))
	}
	want := extension.ProjectTrustContext{Cwd: "/project", Mode: extension.ModeRPC, HasUI: true, UI: extension.NoopUIContext}
	for i, wantDecision := range []extension.ProjectTrustEventDecision{extension.ProjectTrustUndecided, extension.ProjectTrustYes} {
		result, err := f.OnProjectTrustHandlers[i](context.Background(), extension.ProjectTrustEvent{Type: "project_trust", Cwd: "/project"}, want)
		if err != nil || result.Trusted != wantDecision {
			t.Fatalf("handler %d: %+v, %v", i, result, err)
		}
	}
	if len(seen) != 2 || seen[0] != want || seen[1] != want {
		t.Fatalf("handlers saw %+v", seen)
	}
}
