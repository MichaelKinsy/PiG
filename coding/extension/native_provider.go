package extension

import (
	"context"
	"encoding/json"

	"github.com/MichaelKinsy/PiG/ai"
)

// NativeProvider is a session-owned carrier for Pi's native Provider callbacks.
// Metadata is immutable; refresh publishes replacement model snapshots. The host
// owns credentials and persistence, and every callback receives its operation context.
type NativeProvider struct {
	// IsCurrent guards publication when host registrations overlap across connections.
	IsCurrent                func() bool
	ID                       string
	Name                     string
	BaseURL                  string
	Models                   []ProviderModelConfig
	CheckAuth                func(context.Context, *ai.Credential) (*ai.AuthCheck, error)
	FilterModels             func(context.Context, []ProviderModelConfig, *ai.Credential) ([]ProviderModelConfig, error)
	ResolveAuth              func(context.Context, *ai.Credential, ai.AuthResolutionOverrides) (*ai.AuthResult, *ai.Credential, error)
	ResolveRefreshCredential func(context.Context, *ai.Credential) (*ai.Credential, *ai.Credential, error)
	RefreshModels            func(context.Context, *ai.Credential, *ai.ModelsStoreEntry, bool, *bool, func(NativeProviderPublication) error) ([]ProviderModelConfig, error)
	Stream                   func(context.Context, *ai.Model, ai.TranscriptContext, ai.StreamOptions, bool) (*ai.AssistantMessageEventStream, error)
}

type NativeProviderPublication struct {
	Persist json.RawMessage        `json:"persist,omitempty"`
	Models  *[]ProviderModelConfig `json:"models,omitempty"`
}

type providerStreamMethodKey struct{}

// WithProviderStreamSimple carries the selected Pi stream method through the host request.
func WithProviderStreamSimple(ctx context.Context, simple bool) context.Context {
	return context.WithValue(ctx, providerStreamMethodKey{}, simple)
}
func ProviderStreamSimpleSelected(ctx context.Context) bool {
	value, _ := ctx.Value(providerStreamMethodKey{}).(bool)
	return value
}
