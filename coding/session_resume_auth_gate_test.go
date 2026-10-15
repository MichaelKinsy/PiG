package coding

import (
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/sdk.ts:224-231 keeps a saved model only when modelRuntime.hasConfiguredAuth(model.provider); otherwise the
// session starts on findInitialModel's choice, here none, and the Session reports the "unknown" placeholder model that Pi's RPC get_state shows (comb-audit row 19).
func TestResumedSessionDoesNotRestoreAModelWithoutAuth(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_OAUTH_TOKEN", "")
	authed := newTestServices(t)
	if err := authed.Auth().Set("anthropic", ai.Credential{Type: ai.CredentialAPIKey, Key: "saved-key"}); err != nil {
		t.Fatal(err)
	}
	if refreshed := authed.ModelRuntime().Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)}); refreshed.Aborted || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh=%v", refreshed)
	}
	saved, err := BuildModel("anthropic/claude-opus-4-8", authed)
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewSession(authed, SessionOptions{Model: saved, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "hello"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, API: ai.APIAnthropicMessages, Provider: "anthropic", ModelID: "claude-opus-4-8", Content: []ai.AssistantContentBlock{ai.TextContent{Text: "hi"}}, StopReason: ai.StopReasonStop}},
	} {
		if _, err := source.Inner().AppendMessage(message); err != nil {
			t.Fatal(err)
		}
	}
	path := source.Path()
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	for range source.Events() {
	}

	unauthed := newTestServices(t)
	resumed, err := NewSession(unauthed, SessionOptions{ResumePath: path, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = resumed.Close()
		for range resumed.Events() {
		}
	})
	if got := resumed.Model(); got == nil || got.ID != "unknown" || got.ProviderMeta.ProviderID != "unknown" {
		t.Errorf("resumed without auth on %+v, want the unknown placeholder", got)
	}

	// Session replacement (resume, switch, fork) applies the same gate.
	replacement, err := NewSession(unauthed, SessionOptions{Model: &ai.Model{ID: "keep", ProviderMeta: ai.ProviderMetadata{ProviderID: "custom-proxy", API: ai.APIOpenAICompletions, BaseURL: "http://127.0.0.1:9/v1"}}, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = replacement.Close()
		for range replacement.Events() {
		}
	})
	replacement.ReplaceInner(resumed.Inner())
	if got := replacement.Model(); got == nil || got.ID != "keep" {
		t.Errorf("replacement restored a model without auth: %+v", got)
	}
}
