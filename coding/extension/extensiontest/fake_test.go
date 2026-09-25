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
	cancel1 := bus.Subscribe("topic", func(p any) {
		got = append(got, "h1:"+p.(string))
	})
	cancel2 := bus.Subscribe("topic", func(p any) {
		got = append(got, "h2:"+p.(string))
	})
	defer cancel2()

	bus.Publish("topic", "hello")
	if len(got) != 2 || got[0] != "h1:hello" || got[1] != "h2:hello" {
		t.Fatalf("after publish: got %v", got)
	}

	cancel1()
	bus.Publish("topic", "world")
	if len(got) != 3 || got[2] != "h2:world" {
		t.Fatalf("after cancel: got %v", got)
	}
}
