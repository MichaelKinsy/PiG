package coding

import (
	"context"
	"reflect"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

func TestNativeOverlayRetainsLoginAndRuntimeAvailability(t *testing.T) {
	services, _ := nativeCompatServices(t, `{"providers":{"native-overlay":{"baseUrl":"https://overlay.test"}}}`, nil)
	model := nativeCompatModel("one", "native-overlay", "https://native.test")
	provider := nativeCompatProvider(model)
	provider.Auth.APIKey.Login = func(ctx context.Context, interaction ai.AuthInteraction) (ai.Credential, error) {
		key, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "native prompt"})
		return ai.Credential{Type: ai.CredentialAPIKey, Key: key}, err
	}
	if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
		t.Fatal(err)
	}
	called := false
	_, err := services.ModelRuntime().Login(t.Context(), provider.ID, ai.CredentialAPIKey, ai.AuthInteraction{Prompt: func(_ context.Context, prompt ai.AuthPrompt) (string, error) {
		called = true
		if !reflect.DeepEqual(prompt, ai.AuthSecretPrompt{Message: "native prompt"}) {
			t.Errorf("prompt=%+v", prompt)
		}
		return "secret", nil
	}})
	if err != nil || !called {
		t.Fatalf("login called=%v err=%v", called, err)
	}
	if !services.Registry().HasConfiguredAuth(provider.ID) {
		t.Fatal("native auth missing from cached availability")
	}
	found := false
	for _, model := range services.Registry().RuntimeModels() {
		if model.Provider == provider.ID && model.ID == "one" {
			found = true
		}
	}
	if !found {
		t.Fatal("native model missing from interactive runtime snapshot")
	}
	entry, exists := services.Registry().Resolve(provider.ID, model.ID)
	if !exists || entry.BaseURL != "https://overlay.test" {
		t.Fatalf("native registry resolution = %+v, %v", entry, exists)
	}
	found = false
	for _, entry := range services.Registry().GetAll() {
		if entry.ProviderID == provider.ID && entry.ModelID == model.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("native model missing from full registry catalog")
	}
}

func TestNativeRefreshCancellationRejectsLatePublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// Services created in the bubble own the registration refresh admitted there.
		services, _ := nativeCompatServices(t, "", nil)
		started, release, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
		provider := nativeCompatProvider(nativeCompatModel("base", "native-cancel", "https://fixture.invalid"))
		mutated := false
		provider.RefreshModels = func(refresh ai.RefreshModelsContext) error {
			close(started)
			<-release
			defer close(settled)
			_, err := refresh.Publish(ai.ModelsPublication{Update: func() { mutated = true }})
			return err
		}
		if err := services.ModelRuntime().RegisterNativeProvider(provider); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan ai.ModelsRefreshResult, 1)
		go func() {
			result <- services.ModelRuntime().Refresh(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		}()
		<-started
		cancel()
		if got := <-result; !got.Aborted {
			t.Fatalf("refresh=%+v", got)
		}
		close(release)
		<-settled
		synctest.Wait()
		if mutated {
			t.Fatal("cancelled publication mutated catalog")
		}
	})
}

func BenchmarkNativeModelLookup(b *testing.B) {
	services, err := NewServices(ServicesOptions{CWD: b.TempDir(), AgentDir: b.TempDir()})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(services.Close)
	model := nativeCompatModel("native", "benchmark-native", "https://fixture.invalid")
	if err := services.ModelRuntime().RegisterNativeProvider(nativeCompatProvider(model)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if services.ModelRuntime().GetModel("benchmark-native", "native") == nil {
			b.Fatal("model missing")
		}
	}
}
