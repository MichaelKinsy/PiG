package coding

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Upstream createAgentSessionRuntime keeps the factory's { session, services } as the runtime's current pair, and a replacement installs the pair the factory returns for the new Session.
func TestRuntimeAdoptsTheFactoryResultSessionAndServices(t *testing.T) {
	h := newRuntimeTestHarness(t, runtimeTestOptions{})
	firstSession, firstServices := h.runtime.Session(), h.runtime.Services()
	if firstSession == nil || firstServices == nil {
		t.Fatalf("runtime pair = %v, %v; want the factory result", firstSession, firstServices)
	}
	if firstServices.CWD() != firstSession.CWD() {
		t.Errorf("Services.CWD = %q, Session.CWD = %q; one factory result must pair them", firstServices.CWD(), firstSession.CWD())
	}

	result, err := h.runtime.NewSession(t.Context(), nil)
	if err != nil || result.Cancelled {
		t.Fatalf("NewSession = %+v, %v", result, err)
	}
	secondSession, secondServices := h.runtime.Session(), h.runtime.Services()
	if secondSession == firstSession || secondServices == firstServices {
		t.Errorf("replacement kept the old pair: session same=%v services same=%v", secondSession == firstSession, secondServices == firstServices)
	}
	if secondSession == nil || secondServices == nil || secondServices.CWD() != secondSession.CWD() {
		t.Errorf("replacement pair = %v, %v; want the new factory result", secondSession, secondServices)
	}
}

// agent-session-runtime.ts runs beforeSessionInvalidate after the outgoing Session's session_shutdown event and before it invalidates the extension runner, both when a replacement tears the Session down and when dispose quits.
// Pi: packages/coding-agent/src/core/agent-session-runtime.ts:129 (Runtime.setBeforeSessionInvalidate).
func TestRuntimeBeforeSessionInvalidateRunsBetweenShutdownAndInvalidation(t *testing.T) {
	var order []string
	h := newRuntimeTestHarness(t, runtimeTestOptions{extension: func() extension.Extension {
		return extension.Extension{Handlers: map[string][]extension.HandlerFn{
			"session_shutdown": {func(args ...any) (any, error) {
				order = append(order, "shutdown:"+args[0].(extension.SessionShutdownEvent).Reason)
				return nil, nil
			}},
		}}
	}})
	h.runtime.SetBeforeSessionInvalidate(func() { order = append(order, "invalidate") })

	if _, err := h.runtime.NewSession(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if want := []string{"shutdown:new", "invalidate"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("replacement order = %v, want %v", order, want)
	}
	order = nil
	if err := h.runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"shutdown:quit", "invalidate"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("dispose order = %v, want %v", order, want)
	}
}
