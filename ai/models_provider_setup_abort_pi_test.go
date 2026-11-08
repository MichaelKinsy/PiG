package ai

import (
	"context"
	"testing"
)

// models.ts createProvider (:1112-1130, :1170-1190, Pi f3a64af07 #8635) hands the request signal to lazyStream at the two sites that
// reject without calling a provider: a model whose API the provider does not implement, and fetchDeferred on one without deferred
// support. lazy.ts:83 then reports the setup failure as "aborted" under an aborted signal and as "error" otherwise, with the same message.
func TestModelsProviderSetupFailureReasonFollowsTheRequestSignal(t *testing.T) {
	auth := ProviderAuth{APIKey: &APIKeyAuth{Name: "Test", Resolve: func(context.Context, APIKeyAuthInput) (*AuthResult, error) { return &AuthResult{}, nil }}}
	deferred := func(context.Context, *Model, DeferredHandle, DeferredFetchOptions) (*AssistantMessageEventStream, error) {
		return NewAssistantMessageEventStream(), nil
	}
	provider := CreateProvider(CreateProviderOptions{ID: "demo", Auth: auth,
		API: ProviderAPIMap{
			"implemented": &ProviderStreams{Stream: typedChatStreams().Stream, StreamSimple: typedChatStreams().StreamSimple, FetchDeferred: deferred},
			"no-deferred": typedChatStreams(),
		}})
	model := func(api API) *Model {
		return &Model{ID: "m", ProviderMeta: ProviderMetadata{ProviderID: "demo", API: api}}
	}
	transcript := NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
	for _, tc := range []struct {
		name    string
		call    func(context.Context) (*AssistantMessageEventStream, error)
		message string
	}{
		{"stream without an API implementation", func(ctx context.Context) (*AssistantMessageEventStream, error) {
			return provider.Stream(ctx, model("missing"), transcript, StreamOptions{})
		}, `Provider demo has no API implementation for "missing"`},
		{"streamSimple without an API implementation", func(ctx context.Context) (*AssistantMessageEventStream, error) {
			return provider.StreamSimple(ctx, model("missing"), transcript, StreamOptions{})
		}, `Provider demo has no API implementation for "missing"`},
		{"fetchDeferred without deferred support", func(ctx context.Context) (*AssistantMessageEventStream, error) {
			return provider.FetchDeferred(ctx, model("no-deferred"), DeferredHandle{}, DeferredFetchOptions{})
		}, `Provider demo does not support deferred responses for "no-deferred"`},
	} {
		for _, cancelled := range []bool{false, true} {
			ctx, cancel := context.WithCancel(t.Context())
			if cancelled {
				cancel()
			}
			stream, err := tc.call(ctx)
			cancel()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			want := StopReasonError
			if cancelled {
				want = StopReasonAborted
			}
			if result := stream.Result(); result.StopReason != want || result.ErrorMessage != tc.message {
				t.Errorf("%s cancelled=%v: result = %v %q, want %v %q", tc.name, cancelled, result.StopReason, result.ErrorMessage, want, tc.message)
			}
		}
	}
}
