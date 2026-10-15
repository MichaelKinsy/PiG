package coding

import (
	"context"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// upstream: packages/coding-agent/src/core/agent-session.ts:1680-1697 — a registered provider's actual auth contract, not a builtin name policy, decides prompt admission.
func TestPromptHonorsRegisteredProviderAuthContract(t *testing.T) {
	for _, id := range []string{"amazon-bedrock", "ollama", "custom-auth-required"} {
		t.Run(id, func(t *testing.T) {
			dir := t.TempDir()
			services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(services.Close)
			provider := &scriptedProvider{responses: []scriptedResponse{fauxReply("must not run", ai.StopReasonStop, 0)}}
			model := nativeCompatModel("auth-probe", id, "https://unused.invalid")
			stream := func(ctx context.Context, _ *ai.Model, request ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
				return provider.Stream(ctx, request, options)
			}
			native := &ai.ModelsProvider{ID: id, Name: id,
				GetModels: func() ([]*ai.Model, error) { return []*ai.Model{model}, nil },
				Stream:    stream, StreamSimple: stream,
				Auth: ai.ProviderAuth{APIKey: &ai.APIKeyAuth{Name: "Required credential", Resolve: func(context.Context, ai.APIKeyAuthInput) (*ai.AuthResult, error) { return nil, nil }}},
			}
			if err := services.ModelRuntime().RegisterNativeProvider(native); err != nil {
				t.Fatal(err)
			}
			result := services.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
			if result.Aborted || len(result.Errors) > 0 {
				t.Fatal(result)
			}
			session, err := NewSession(services, SessionOptions{Model: services.ModelRuntime().GetModel(id, model.ID), SkipBuiltinTools: true, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for event := range session.Events() {
					AcknowledgeEvent(event)
				}
			}()
			t.Cleanup(func() {
				if err := session.Close(); err != nil {
					t.Error(err)
				}
				<-done
			})
			err = session.Prompt(t.Context(), "hi")
			if err == nil || !strings.Contains(err.Error(), "No API key found for "+id+".") || provider.callCount() != 0 {
				t.Fatalf("missing configured auth: error=%v provider calls=%d", err, provider.callCount())
			}
		})
	}
}
