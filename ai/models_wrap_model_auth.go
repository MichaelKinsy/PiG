package ai

import "context"

// ModelAuthFunc resolves the request auth of one chat model (CreateModelsWithModelAuth's callback).
type ModelAuthFunc = func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error)

// WrapModelAuth replaces the model-level auth resolution of chat models with wrap of the current one (the callback given
// at creation, else the provider's auth with the model's own headers merged in): a host that composes configuration over
// a collection it did not create adds its model-specific request headers this way, as an application overrides `getAuth`
// on a Pi `Models` instance. Call it before the collection serves requests.
func (m *Models) WrapModelAuth(wrap func(previous ModelAuthFunc) ModelAuthFunc) {
	previous := m.modelAuth
	if previous == nil {
		previous = func(ctx context.Context, model *Model, overrides AuthResolutionOverrides) (*AuthResult, error) {
			return m.providerModelAuth(ctx, model, overrides)
		}
	}
	m.modelAuth = wrap(previous)
}
