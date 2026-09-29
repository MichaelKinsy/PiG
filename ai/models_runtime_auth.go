package ai

import (
	"context"
	"fmt"
	"slices"
)

func (m *Models) supersedeProviderRefreshLocked(id string) uint64 {
	m.refreshGenerations[id]++
	if previous := m.refreshControllers[id]; previous != nil {
		delete(m.refreshControllers, id)
		previous.cancel(context.Canceled)
	}
	return m.refreshGenerations[id]
}

func (m *Models) SetProvider(provider *ModelsProvider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supersedeProviderRefreshLocked(provider.ID)
	if _, exists := m.providers[provider.ID]; !exists {
		m.order = append(m.order, provider.ID)
	}
	m.providers[provider.ID] = provider
}

func (m *Models) DeleteProvider(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.supersedeProviderRefreshLocked(id)
	delete(m.providers, id)
	m.order = slices.DeleteFunc(m.order, func(value string) bool { return value == id })
}

func (m *Models) ClearProviders() {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := map[string]bool{}
	for id := range m.providers {
		ids[id] = true
	}
	for id := range m.refreshControllers {
		ids[id] = true
	}
	for id := range ids {
		m.supersedeProviderRefreshLocked(id)
	}
	clear(m.providers)
	m.order = nil
}

func (m *Models) GetProviders() []*ModelsProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*ModelsProvider, 0, len(m.order))
	for _, id := range m.order {
		out = append(out, m.providers[id])
	}
	return out
}

func (m *Models) GetProvider(id string) *ModelsProvider {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.providers[id]
}

func (m *Models) GetModels(provider ...string) []*Model {
	var providers []*ModelsProvider
	if len(provider) > 0 {
		providers = []*ModelsProvider{m.GetProvider(provider[0])}
	} else {
		providers = m.GetProviders()
	}
	out := []*Model{}
	for _, provider := range providers {
		if provider == nil || provider.GetModels == nil {
			continue
		}
		models, err := provider.GetModels()
		if err == nil {
			out = append(out, models...)
		}
	}
	return out
}

func (m *Models) GetModel(provider, id string) *Model {
	for _, model := range m.GetModels(provider) {
		if model.ID == id {
			return model
		}
	}
	return nil
}

func HasApi(model *Model, api API) bool { return model != nil && model.ProviderMeta.API == api }

func (m *Models) GetAuth(ctx context.Context, providerID string, overrides ...AuthResolutionOverrides) (*AuthResult, error) {
	provider := m.GetProvider(providerID)
	if provider == nil {
		return nil, nil
	}
	var opts AuthResolutionOverrides
	if len(overrides) > 0 {
		opts = overrides[0]
	}
	return awaitModelsOperation(ctx, &m.operations, func() (*AuthResult, error) {
		return ResolveProviderAuth(ctx, provider.ID, provider.Auth, m.credentials, m.authContext, opts)
	})
}

func (m *Models) GetModelAuth(ctx context.Context, model *Model, overrides ...AuthResolutionOverrides) (*AuthResult, error) {
	if m.modelAuth != nil {
		var opts AuthResolutionOverrides
		if len(overrides) > 0 {
			opts = overrides[0]
		}
		return m.modelAuth(ctx, model, opts)
	}
	result, err := m.GetAuth(ctx, modelProviderID(model), overrides...)
	if err != nil || result == nil || model.ProviderMeta.Headers == nil {
		return result, err
	}
	result = new(*result)
	headers := make(ProviderHeaders, len(model.ProviderMeta.Headers))
	for key, value := range model.ProviderMeta.Headers {
		headers[key] = new(value)
	}
	result.Auth.Headers = MergeProviderHeaders(result.Auth.Headers, headers)
	return result, nil
}

func (m *Models) CheckAuth(ctx context.Context, providerID string) (*AuthCheck, error) {
	return awaitModelsOperation(ctx, &m.operations, func() (*AuthCheck, error) {
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		provider := m.GetProvider(providerID)
		if provider == nil {
			return nil, nil
		}
		return CheckProviderAuth(ctx, provider.ID, provider.Auth, m.credentials, m.authContext)
	})
}

func (m *Models) GetAvailable(ctx context.Context, providerID ...string) ([]*Model, error) {
	return awaitModelsOperation(ctx, &m.operations, func() ([]*Model, error) {
		providers := m.GetProviders()
		if len(providerID) > 0 && providerID[0] != "" {
			providers = nil
			if provider := m.GetProvider(providerID[0]); provider != nil {
				providers = []*ModelsProvider{provider}
			}
		}
		type checked struct {
			index      int
			credential *Credential
			auth       *AuthCheck
			err        error
		}
		checks := make([]checked, len(providers))
		completed := make(chan checked, len(providers))
		for i, provider := range providers {
			m.operations.Go(func() {
				credential, err := readProviderCredential(ctx, m.credentials, provider.ID)
				if err != nil {
					completed <- checked{index: i, err: err}
					return
				}
				check, err := m.checkProviderAuth(ctx, provider, credential)
				completed <- checked{i, credential, check, err}
			})
		}
		for range providers {
			check := <-completed
			if check.err != nil {
				return nil, check.err
			}
			checks[check.index] = check
		}
		out := []*Model{}
		for i, provider := range providers {
			check := checks[i]
			if check.auth == nil {
				continue
			}
			models, err := provider.GetModels()
			if err != nil {
				return nil, err
			}
			if provider.FilterModels != nil {
				models = provider.FilterModels(models, check.credential)
			}
			out = append(out, models...)
		}
		return out, nil
	})
}

func (m *Models) checkProviderAuth(ctx context.Context, provider *ModelsProvider, credential *Credential) (*AuthCheck, error) {
	if credential != nil && credential.Type == CredentialOAuth {
		if provider.Auth.OAuth == nil {
			return nil, nil
		}
		return &AuthCheck{Source: "OAuth", Type: CredentialOAuth}, nil
	}
	if provider.Auth.APIKey == nil {
		return nil, nil
	}
	if provider.Auth.APIKey.Check != nil {
		input := APIKeyAuthInput{Ctx: m.authContext}
		if credential != nil && credential.Type == CredentialAPIKey {
			input.Credential = credential
		}
		check, err := provider.Auth.APIKey.Check(ctx, input)
		if err != nil {
			return nil, NewModelsError(ModelsErrorAuth, "API key auth check failed for provider "+provider.ID, err)
		}
		return check, nil
	}
	resolution, err := ResolveProviderAuth(ctx, provider.ID, provider.Auth, m.credentials, m.authContext, AuthResolutionOverrides{})
	if err != nil || resolution == nil {
		return nil, err
	}
	return &AuthCheck{Source: resolution.Source, Type: CredentialAPIKey}, nil
}

func (m *Models) Login(ctx context.Context, providerID string, authType AuthType, interaction AuthInteraction) (Credential, error) {
	if ctx.Err() != nil {
		return Credential{}, context.Cause(ctx)
	}
	provider := m.GetProvider(providerID)
	if provider == nil {
		return Credential{}, NewModelsError(ModelsErrorProvider, "Unknown provider: "+providerID, nil)
	}
	var login func(context.Context, AuthInteraction) (Credential, error)
	if authType == CredentialOAuth && provider.Auth.OAuth != nil {
		login = provider.Auth.OAuth.Login
	} else if authType != CredentialOAuth && provider.Auth.APIKey != nil {
		login = provider.Auth.APIKey.Login
	}
	if login == nil {
		return Credential{}, NewModelsError(ModelsErrorAuth, fmt.Sprintf("%s does not support %s login", provider.Name, authType), nil)
	}
	credential, err := awaitModelsOperation(ctx, &m.operations, func() (Credential, error) { return login(ctx, interaction) })
	if err != nil {
		return Credential{}, err
	}
	started := make(chan struct{})
	mutation := make(chan error, 1)
	m.operations.Go(func() {
		_, err := m.credentials.Modify(ctx, providerID, func(*Credential) (*Credential, error) { close(started); return new(credential), nil })
		mutation <- err
	})
	select {
	case <-started:
		err = <-mutation
	case err = <-mutation:
	case <-ctx.Done():
		select {
		case <-started:
			err = <-mutation
		default:
			return Credential{}, context.Cause(ctx)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return Credential{}, context.Cause(ctx)
		}
		return Credential{}, NewModelsError(ModelsErrorAuth, "Credential store modify failed for "+providerID, err)
	}
	return credential, nil
}

func (m *Models) Logout(ctx context.Context, providerID string) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}
	err := m.credentials.Delete(ctx, providerID)
	if err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return NewModelsError(ModelsErrorAuth, "Credential store delete failed for "+providerID, err)
	}
	return nil
}
