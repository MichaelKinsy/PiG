package inproc

import (
	"context"
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's bindCore takes providerActions.registerNativeProvider(provider: Provider) (runner.ts:410-418) and hands it each queued registration's Provider
// object, then the later ones (runner.ts:481-490). Go's ProviderActions.RegisterNativeProvider takes that object: a compiled-in extension's
// registration arrives as the very object it registered, a subprocess carrier's as the object assembled from the carrier, and the action wins
// over the model registry's own carrier registration.
func TestBindCoreRegisterNativeProviderTakesThePiProviderObject(t *testing.T) {
	runtime := extension.CreateExtensionRuntime()
	queued := &ai.ModelsProvider{ID: "queued", Name: "Queued"}
	if err := runtime.RegisterNativeProvider(context.Background(), queued, "/ext/a.ts"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.RegisterNativeProviderCarrier(context.Background(), &extension.NativeProvider{ID: "sub", Name: "Subprocess"}, "/ext/b.py"); err != nil {
		t.Fatal(err)
	}
	var got []*ai.ModelsProvider
	var errs []*extension.ExtensionError
	runner := NewRunner(nil, t.TempDir(), runtime)
	runner.AddErrorListener(func(err *extension.ExtensionError) { errs = append(errs, err) })
	registry := &providerRegistryStub{}
	runner.BindCore(extension.ExtensionActions{}, extension.ContextActions{ModelRegistry: registry}, &extension.ProviderActions{
		RegisterNativeProvider: func(_ context.Context, provider *ai.ModelsProvider) error {
			got = append(got, provider)
			if provider.ID == "sub" {
				return errors.New("sub refused")
			}
			return nil
		},
	})
	later := &ai.ModelsProvider{ID: "later"}
	if err := runtime.RegisterNativeProvider(context.Background(), later); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != queued || got[1].ID != "sub" || got[1].Name != "Subprocess" || got[2] != later {
		t.Fatalf("registerNativeProvider received %+v, want the queued object itself, the subprocess carrier's object, then the later object", got)
	}
	if len(registry.registered) != 0 {
		t.Fatalf("the model registry registered %v although providerActions.registerNativeProvider was given", registry.registered)
	}
	if len(errs) != 1 || errs[0].ExtensionPath != "/ext/b.py" || errs[0].Event != "register_provider" || errs[0].Error != "sub refused" {
		t.Fatalf("errors = %+v, want the refused queued registration reported for its extension (runner.ts:491-497)", errs)
	}
}
