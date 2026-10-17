package coding

import (
	"sync/atomic"
	"testing"

	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

type publishCountingBridge struct {
	actions   map[string]any
	published atomic.Int32
}

func (bridge *publishCountingBridge) SetHostAction(key string, action any) {
	bridge.actions[key] = action
}
func (bridge *publishCountingBridge) PublishModelCatalog() { bridge.published.Add(1) }

// The extension model surface republishes its catalog and drops its typed-model cache on every committed registry change. ModelRuntime.SetChangeListener is public and single-slot, so a caller that installs its own listener must not silence that subscription.
func TestPublicChangeListenerDoesNotSilenceTheExtensionCatalogPublication(t *testing.T) {
	services := newRegistrationServices(t)
	bridge := &publishCountingBridge{actions: map[string]any{}}
	detachOperations := icodingagent.WireModelOperations(bridge, icodingagent.ModelOperationBindings{Registry: services.Registry().ModelRegistry})
	defer detachOperations()
	published := bridge.published.Load()

	var callerNotified atomic.Int32
	detachCaller := services.ModelRuntime().SetChangeListener(func() { callerNotified.Add(1) })
	defer detachCaller()
	if err := services.Registry().RegisterExtensionProvider("listener-provider", extensionRegistration("https://listener.invalid/v1", "test-key", "one")); err != nil {
		t.Fatal(err)
	}
	if callerNotified.Load() == 0 {
		t.Fatal("the caller's listener was not notified")
	}
	if got := bridge.published.Load(); got <= published {
		t.Fatalf("catalog published %d times after a committed registry change, want more than %d: the caller's listener replaced the extension subscription", got, published)
	}
}
