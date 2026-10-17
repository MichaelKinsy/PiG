package codingagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// model-registry.ts getAvailable: only models of providers with configured auth are listed, and a Copilot OAuth credential's model picker list further restricts that provider's models; getAll lists every model of every provider in provider order.
// Pi: packages/coding-agent/src/core/model-registry.ts getAll, getAvailable.
func TestGetAvailableModelDataFiltersByConfiguredAuthAndCopilotAllowlist(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "")
	dir := t.TempDir()
	store, err := ai.NewAuthStorage(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	copilot := ai.ListModels("github-copilot")
	if len(copilot) < 2 {
		t.Fatalf("the catalog lists %d github-copilot models; the allowlist case needs two", len(copilot))
	}
	allowed := copilot[1].ID
	ids, _ := json.Marshal([]string{allowed})
	if err := store.Set("github-copilot", ai.Credential{Type: ai.CredentialOAuth, Access: "fixture", AvailableModelIDs: ids}); err != nil {
		t.Fatal(err)
	}
	modelsJSON := `{"providers":{"custom-authed":{"baseUrl":"http://127.0.0.1:9","api":"openai-completions","apiKey":"x","models":[{"id":"one"},{"id":"two"}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "models.json"), []byte(modelsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	registry := NewModelRegistry(dir)
	t.Cleanup(registry.CloseModelTasks)
	registry.SetAuthStorage(store)

	byProvider := map[string][]string{}
	for _, model := range registry.GetAvailableModelData() {
		byProvider[model.ProviderMeta.ProviderID] = append(byProvider[model.ProviderMeta.ProviderID], model.ID)
	}
	if got := byProvider["github-copilot"]; !slices.Equal(got, []string{allowed}) {
		t.Errorf("Copilot models = %v, want only the credential's allowlist [%s]", got, allowed)
	}
	if got := byProvider["custom-authed"]; !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("custom provider with an API key = %v", got)
	}
	if got := byProvider["mistral"]; len(got) != 0 {
		t.Errorf("a provider without auth listed %v", got)
	}

	all := map[string]int{}
	var order []string
	for _, model := range registry.GetAllModelData() {
		if all[model.ProviderMeta.ProviderID] == 0 {
			order = append(order, model.ProviderMeta.ProviderID)
		}
		all[model.ProviderMeta.ProviderID]++
	}
	if all["github-copilot"] != len(copilot) || all["mistral"] == 0 || all["custom-authed"] != 2 {
		t.Errorf("GetAllModelData counts = %v, want every model (copilot %d)", all, len(copilot))
	}
	ref := registry.modelProviderIDs()
	var wantOrder []string
	for _, id := range ref {
		if all[id] > 0 {
			wantOrder = append(wantOrder, id)
		}
	}
	if !slices.Equal(order, wantOrder) {
		t.Errorf("GetAllModelData provider order = %v, want %v", order, wantOrder)
	}
}

type refreshCall struct {
	allowNetwork bool
	force        *bool
}

func refreshProbeProvider(id string, failNetwork error, mu *sync.Mutex, calls *[]refreshCall) *ai.ModelsProvider {
	p := credentialTestProvider(id)
	p.RefreshModels = func(c ai.RefreshModelsContext) error {
		mu.Lock()
		*calls = append(*calls, refreshCall{c.AllowNetwork, c.Force})
		mu.Unlock()
		if c.AllowNetwork {
			return failNetwork
		}
		return nil
	}
	return p
}

// model-runtime.ts refresh: network refresh is on unless allowNetwork is false, force and the provider selection reach the provider, provider errors are returned with their order, an aborted refresh reports an empty error map, and a refresh scoped to some providers covers only their queued registration refreshes and re-queues them when aborted.
// Pi: packages/coding-agent/src/core/model-runtime.ts refresh (:701-796), models.ts refresh (:398-449).
func TestRefreshModelRuntimeHonoursNetworkForceErrorsAndRegistrationCover(t *testing.T) {
	var mu sync.Mutex
	var calls []refreshCall
	boom := errors.New("boom")
	probe := refreshProbeProvider("refresh-probe", boom, &mu, &calls)
	other := refreshProbeProvider("refresh-other", nil, &mu, &calls)
	registry := credentialTestRuntime(t, ai.NewInMemoryAuthStorage(nil), probe, other)
	registry.SetRuntimeAPIKey(probe.ID, "key")
	registry.SetRuntimeAPIKey(other.ID, "key")
	reset := func() { mu.Lock(); calls = nil; mu.Unlock() }
	networkCalls := func() (n int, forced *bool) {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range calls {
			if c.allowNetwork {
				n++
				forced = c.force
			}
		}
		return n, forced
	}

	reset()
	result := registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{Providers: []string{probe.ID}})
	if n, _ := networkCalls(); n != 1 {
		t.Fatalf("a refresh without allowNetwork made %d network refreshes of the provider, want 1", n)
	}
	if result.Aborted || !errors.Is(result.Errors[probe.ID], boom) || !slices.Equal(result.ErrorOrder, []string{probe.ID}) {
		t.Fatalf("result = %+v, want the provider's error and its order", result)
	}

	reset()
	registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{probe.ID}})
	if n, _ := networkCalls(); n != 0 {
		t.Fatalf("allowNetwork=false made %d network refreshes", n)
	}

	reset()
	registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(true), Force: new(true), Providers: []string{probe.ID}})
	if n, forced := networkCalls(); n != 1 || forced == nil || !*forced {
		t.Fatalf("force did not reach the provider: %d calls, force %v", n, forced)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	aborted := registry.RefreshModelRuntime(ctx, ai.ModelsRefreshOptions{Providers: []string{probe.ID}})
	if !aborted.Aborted || aborted.Errors == nil || len(aborted.Errors) != 0 {
		t.Fatalf("a cancelled refresh = %+v, want aborted with an empty non-nil error map", aborted)
	}

	// Registration cover: a held registration refresh stays queued for providers a scoped refresh does not select.
	release := registry.HoldRegistrationRefresh()
	defer release()
	registry.deferRegistrationRefresh(probe.ID)
	registry.deferRegistrationRefresh(other.ID)
	queued := func(id string) bool {
		registry.registrationRefresh.mu.Lock()
		defer registry.registrationRefresh.mu.Unlock()
		_, ok := registry.registrationRefresh.providers[id]
		return ok
	}
	registry.RefreshModelRuntime(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{probe.ID}})
	if queued(probe.ID) || !queued(other.ID) {
		t.Fatalf("after a scoped refresh: selected queued=%v, unselected queued=%v; want the selected provider covered and the other left queued", queued(probe.ID), queued(other.ID))
	}
	registry.RefreshModelRuntime(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{other.ID}})
	if !queued(other.ID) {
		t.Fatal("an aborted scoped refresh did not return its covered provider to the queue")
	}
	registry.registrationRefresh.mu.Lock()
	delete(registry.registrationRefresh.providers, other.ID)
	registry.registrationRefresh.mu.Unlock()
	registry.deferRegistrationRefresh(other.ID)
	registry.RefreshModelRuntime(ctx, ai.ModelsRefreshOptions{AllowNetwork: new(false), Providers: []string{probe.ID}})
	if !queued(other.ID) {
		t.Fatal("a refresh scoped to another provider drained a queued registration")
	}
}
