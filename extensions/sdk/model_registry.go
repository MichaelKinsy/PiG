package sdk

import "slices"

// ProviderAuthStatus identifies the configured credential source without exposing secrets.
type ProviderAuthStatus struct {
	Configured bool   `json:"configured"`
	Source     string `json:"source,omitempty"`
	Label      string `json:"label,omitempty"`
}

type registryProviderState struct {
	Name              string             `json:"name"`
	BaseURL           string             `json:"baseUrl"`
	AuthStatus        ProviderAuthStatus `json:"authStatus"`
	Configured        bool               `json:"configured"`
	UsingOAuth        bool               `json:"usingOAuth"`
	AvailableModelIDs *[]string          `json:"availableModelIds"`
}

type registryState struct {
	Models     []map[string]any                 `json:"models"`
	Providers  map[string]registryProviderState `json:"providers"`
	Error      *string                          `json:"error"`
	Registered []struct {
		Name   string                     `json:"name"`
		Config ProviderConfig             `json:"config"`
		Native *providerObjectDeclaration `json:"native,omitempty"`
	} `json:"registered"`
}

func (r ModelRegistry) state() (registryState, error) {
	return hostValue[registryState](r.context, "getModelRegistryState", nil)
}

func (r ModelRegistry) GetAll() ([]map[string]any, error) {
	state, err := r.state()
	return state.Models, err
}
func (r ModelRegistry) GetAvailable() ([]map[string]any, error) {
	state, err := r.state()
	if err != nil {
		return nil, err
	}
	models := []map[string]any{}
	for _, model := range state.Models {
		provider, _ := model["provider"].(string)
		id, _ := model["id"].(string)
		state := state.Providers[provider]
		if state.Configured && (state.AvailableModelIDs == nil || slices.Contains(*state.AvailableModelIDs, id)) {
			models = append(models, model)
		}
	}
	return models, nil
}
func (r ModelRegistry) GetError() (*string, error) {
	state, err := r.state()
	return state.Error, err
}
func (r ModelRegistry) HasConfiguredAuth(model map[string]any) (bool, error) {
	state, err := r.state()
	provider, _ := model["provider"].(string)
	return state.Providers[provider].Configured, err
}
func (r ModelRegistry) IsUsingOAuth(model map[string]any) (bool, error) {
	state, err := r.state()
	provider, _ := model["provider"].(string)
	return state.Providers[provider].UsingOAuth, err
}
func (r ModelRegistry) GetProviderAuthStatus(provider string) (ProviderAuthStatus, error) {
	state, err := r.state()
	return state.Providers[provider].AuthStatus, err
}
func (r ModelRegistry) GetProviderDisplayName(provider string) (string, error) {
	state, err := r.state()
	if p, ok := state.Providers[provider]; ok {
		return p.Name, err
	}
	return provider, err
}
func (r ModelRegistry) GetProviderAuth(provider string) (map[string]any, error) {
	return hostValue[map[string]any](r.context, "getProviderAuth", map[string]string{"provider": provider})
}

// GetApiKeyForProvider returns nil when auth resolution fails, as Pi does.
func (r ModelRegistry) GetApiKeyForProvider(provider string) *string {
	result, err := r.GetProviderAuth(provider)
	if err != nil {
		return nil
	}
	auth, _ := result["auth"].(map[string]any)
	key, ok := auth["apiKey"].(string)
	if !ok {
		return nil
	}
	return &key
}
func (r ModelRegistry) GetRegisteredProviderIDs() ([]string, error) {
	state, err := r.state()
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(state.Registered))
	for _, entry := range state.Registered {
		ids = append(ids, entry.Name)
	}
	return ids, nil
}
func (r ModelRegistry) GetRegisteredProviderConfig(provider string) (ProviderConfig, error) {
	state, err := r.state()
	if err != nil {
		return nil, err
	}
	for _, entry := range state.Registered {
		if entry.Name == provider {
			return entry.Config, nil
		}
	}
	return nil, nil
}
func (r ModelRegistry) RegisterProvider(name string, config ProviderConfig) error {
	result, err := r.context.callHost("registerProvider", map[string]any{"name": name, "config": config})
	return callResultError(result, err)
}
func (r ModelRegistry) UnregisterProvider(name string) error {
	result, err := r.context.callHost("unregisterProvider", map[string]string{"name": name})
	return callResultError(result, err)
}

type ModelsRefreshOptions struct {
	AllowNetwork *bool    `json:"allowNetwork,omitempty"`
	Providers    []string `json:"providers,omitempty"`
	Force        *bool    `json:"force,omitempty"`
}
type ModelsRefreshResult struct {
	Aborted bool              `json:"aborted"`
	Errors  map[string]string `json:"errors"`
}

// Refresh waits for the host refresh and propagates cancellation through the handler context.
func (r ModelRegistry) Refresh(options ModelsRefreshOptions) (ModelsRefreshResult, error) {
	return hostValue[ModelsRefreshResult](r.context, "refreshModelRegistry", options)
}
