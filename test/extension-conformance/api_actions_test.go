package extensionconformance

import (
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/extensiontest"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// runtimeBackedAPI is an extension.API whose registration members call the runtime the runner binds (loader.ts createExtensionAPI); every other member is the recording double's. It carries only members whose behaviour is the runtime's own queueing and validation: it holds no answer the test assigns.
type runtimeBackedAPI struct {
	*extensiontest.Fake
	runtime *extension.ExtensionRuntime
}

func (a runtimeBackedAPI) RegisterVirtualModel(model extension.ExtensionVirtualModel) {
	_ = a.runtime.RegisterVirtualModel(model, "api-reference")
}

func (a runtimeBackedAPI) UnregisterVirtualModel(provider, id string) {
	a.runtime.UnregisterVirtualModel(provider, id)
}

// TestConformanceAPIVirtualModelsQueueUntilBound is the Go reference for pi.registerVirtualModel and pi.unregisterVirtualModel (loader.ts:225-233, runner.ts:497-541): a virtual model registered before the host binds is queued, an unregister removes it from the queue, and binding hands the survivors to the host's action; a registration after binding applies at once.
//
// mutation-checked: the mutant "ExtensionRuntime.UnregisterVirtualModel leaves the queue untouched" fails it.
func TestConformanceAPIVirtualModelsQueueUntilBound(t *testing.T) {
	t.Parallel()
	runner := inproc.NewRunner(nil, t.TempDir())
	var api extension.API = runtimeBackedAPI{Fake: &extensiontest.Fake{}, runtime: runner.Runtime()}
	api.RegisterVirtualModel(extension.ExtensionVirtualModel{Provider: "p", ID: "a"})
	api.RegisterVirtualModel(extension.ExtensionVirtualModel{Provider: "p", ID: "b"})
	api.UnregisterVirtualModel("p", "a")

	var registered []string
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{}, &extension.ProviderActions{
		RegisterVirtualModel: func(definition extension.VirtualModelDefinition) error {
			registered = append(registered, definition.Provider+"/"+definition.ID)
			return nil
		},
		UnregisterVirtualModel: func(provider, id string) { registered = append(registered, "-"+provider+"/"+id) },
	})
	if !reflect.DeepEqual(registered, []string{"p/b"}) {
		t.Fatalf("binding registered %v, want only the queued p/b: the unregistered p/a must leave the queue", registered)
	}
	api.RegisterVirtualModel(extension.ExtensionVirtualModel{Provider: "p", ID: "c"})
	api.UnregisterVirtualModel("p", "b")
	if want := []string{"p/b", "p/c", "-p/b"}; !reflect.DeepEqual(registered, want) {
		t.Fatalf("after binding the host saw %v, want %v", registered, want)
	}
}
