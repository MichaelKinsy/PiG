package coding

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// Retirement requests follow logical disposal. Physical Host draining remains the mode HostOwner's responsibility.
func TestRuntimeRequestsCapturedResourceRetirementAfterLogicalDisposal(t *testing.T) {
	services := newTestServices(t)
	var order []string
	created := 0
	factory := func(_ context.Context, options CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		created++
		if created == 2 {
			order = append(order, "construct")
		}
		session, err := NewSession(services, SessionOptions{SessionManager: options.SessionManager, Model: fakeModel(), Runner: inproc.NewRunner(nil, services.CWD())})
		if err != nil {
			return CreateAgentSessionRuntimeResult{}, err
		}
		t.Cleanup(func() { _ = session.Close() })
		return CreateAgentSessionRuntimeResult{Session: session, Services: services, Dispose: func(reason string) {
			if !session.currentRunner().IsStale() {
				t.Error("physical retirement requested before logical invalidation")
			}
			select {
			case <-session.closeDone:
			default:
				t.Error("physical retirement requested before Session.Close")
			}
			order = append(order, "dispose:"+reason)
		}}, nil
	}
	manager, err := NewInMemorySessionManager(services.CWD())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := CreateAgentSessionRuntime(t.Context(), factory, CreateAgentSessionRuntimeOptions{CWD: services.CWD(), AgentDir: services.AgentDir(), SessionManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	runtime.SetRebindSession(func(context.Context, *Session) error { order = append(order, "bind"); return nil })
	_, err = runtime.NewSession(t.Context(), &extension.NewSessionOptions{WithSession: func(*extension.ReplacedSessionContext) error { order = append(order, "callback"); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispose:new", "construct", "bind", "callback"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order=%v want=%v", order, want)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"dispose:new", "construct", "bind", "callback", "dispose:quit"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("close order=%v want=%v", order, want)
	}
}

func TestRuntimeConstructionFailureDoesNotRetireOutgoingResourcesTwice(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	retirements := 0
	h.runtime.replacement.current.Load().Dispose = func(string) { retirements++ }
	failure := errors.New("construction failed")
	h.runtime.replacement.createRuntime = func(context.Context, CreateAgentSessionRuntimeOptions) (CreateAgentSessionRuntimeResult, error) {
		return CreateAgentSessionRuntimeResult{}, failure
	}
	if _, err := h.runtime.NewSession(t.Context(), nil); !errors.Is(err, failure) {
		t.Fatalf("new error=%v", err)
	}
	if err := h.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if retirements != 1 {
		t.Fatalf("retirements=%d want one outgoing instance", retirements)
	}
}
