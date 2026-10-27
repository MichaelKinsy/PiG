package subprocess

import (
	"context"
	"errors"
	"net"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture/providerobject"
)

// Pi's loader queues a Provider object an extension registers while it loads, as it queues a configuration, whatever registry the host is bound to: the registry is asked when the
// services apply the queue, and a refusal is the registering extension's diagnostic with the extension still loaded (loader.ts:220-222, runner.ts:488-504). The host bound
// its native registry before the load, as the CLI binds its host at creation. A refused Provider object leaves nothing of its registration in the host.
//
// mutation-checked: registering the Provider object through RegisterNativeProviderCarrier at load (the loading flag false) fails the first assertion; dropping withdrawNativeProvider fails the last.
func TestProviderObjectRegisteredWhileLoadingIsQueuedForABoundRegistry(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		name := map[bool]string{false: "accepted", true: "refused"}[refuse]
		t.Run(name, func(t *testing.T) {
			host := NewHost(t.TempDir())
			t.Cleanup(func() { host.Shutdown("test done") })
			host.SetUIBridge(NewUIBridge(func() {}))
			refusal := errors.New("registry refused the Provider")
			var asked []string
			host.SetProviderCallbacks(func(string, extension.ProviderConfig) error { return nil }, func(string) {})
			host.SetNativeProviderCallback(func(_ context.Context, carrier *extension.NativeProvider) error {
				asked = append(asked, carrier.ID)
				if refuse {
					return refusal
				}
				return nil
			})
			loaded, err := host.LoadInProcess(t.Context(), ExtConfig{Name: "provider-object-go", Path: "/ext/provider-object-go.go", Enabled: true}, func(conn net.Conn) error { return providerobject.Extension().RunWithConn(conn) })
			if err != nil {
				t.Fatalf("the extension must load: %v", err)
			}
			if len(asked) != 0 {
				t.Fatalf("the registry was asked for %v while the extension loaded", asked)
			}
			if queued := host.Runtime().PendingNativeProviderRegistrations(); len(queued) != 1 || queued[0].ExtensionPath != loaded.Path {
				t.Fatalf("queued Provider objects = %+v, want the extension's one", queued)
			}
			failures := applyQueuedRegistrations(host)
			if !slices.Equal(asked, []string{"carrier-provider"}) {
				t.Fatalf("registry asked for %v when the queue drained, want [carrier-provider]", asked)
			}
			host.mu.Lock()
			registered := len(host.nativeProviders) != 0
			for _, providers := range host.nativeProviders {
				registered = registered && providers["carrier-provider"] != nil
			}
			host.mu.Unlock()
			if !refuse {
				if len(failures) != 0 || !registered {
					t.Fatalf("accepted Provider object: failures %+v, host holds it %v", failures, registered)
				}
				return
			}
			if len(failures) != 1 || failures[0].ExtensionPath != loaded.Path || failures[0].Error != refusal.Error() {
				t.Fatalf("diagnostics = %+v, want one for %s: %v", failures, loaded.Path, refusal)
			}
			host.mu.Lock()
			held := 0
			for _, providers := range host.nativeProviders {
				held += len(providers)
			}
			host.mu.Unlock()
			if held != 0 || len(host.ProviderNames("provider-object-go")) != 0 {
				t.Fatalf("a refused Provider object is still held by the host: native %d, owned %v", held, host.ProviderNames("provider-object-go"))
			}
		})
	}
}
