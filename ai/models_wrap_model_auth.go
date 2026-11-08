package ai

import "context"

// ModelAuthFunc resolves the request auth of one chat model (CreateModelsWithModelAuth's callback).
type ModelAuthFunc = func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error)

// WrapModelAuth replaces the model-level auth resolution of chat models with wrap of the current one (the callback given
// at creation, else the provider's auth with the model's own headers merged in): a host that composes configuration over
// a collection it did not create adds its model-specific request headers this way, as an application overrides `getAuth`
// on a Pi `Models` instance. It is safe to call while the collection serves requests: requests that began before it use
// the previous resolution. wrap runs with no lock held.
func (m *Models) WrapModelAuth(wrap func(previous ModelAuthFunc) ModelAuthFunc) {
	m.mu.RLock()
	previous := m.modelAuth
	m.mu.RUnlock()
	if previous == nil {
		previous = func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error) {
			return m.providerModelAuth(ctx, model, overrides)
		}
	}
	wrapped := wrap(previous)
	m.mu.Lock()
	m.modelAuth = wrapped
	m.mu.Unlock()
}
