package coding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/suite/regressions/11520-sdk-summary-oauth-auth.test.ts (Pi 6fed3cfb): a branch summary in a session
// the SDK built lets the stream's own model runtime resolve the stored OAuth login, so no credential is resolved beforehand and passed
// on as an explicit API key.
func TestSDKSummaryLetsTheRuntimeResolveStoredOAuthUpstream(t *testing.T) {
	services := newRegistrationServices(t)
	const providerID = "oauth-only-provider"
	if err := services.Auth().Set(providerID, ai.Credential{Type: ai.CredentialOAuth, Access: "oauth-access", Refresh: "refresh", Expires: 1 << 50}); err != nil {
		t.Fatal(err)
	}
	var requestKeys []string
	input := registryInput("https://example.test/v1", "openai-completions", "oauth-model")
	// With an API key method as well, an explicit key would bypass the stored OAuth login.
	input.APIKey = "UNUSED_ENV_KEY"
	input.OAuth = &ExtensionOAuthConfig{
		Name: "Subscription",
		Login: func(context.Context, ai.OAuthLoginCallbacks) (ai.Credential, error) {
			return ai.Credential{}, errors.New("unused")
		},
		RefreshToken: func(_ context.Context, credential ai.Credential) (ai.Credential, error) { return credential, nil },
		GetAPIKey:    func(credential ai.Credential) string { return credential.Access },
	}
	input.StreamSimple = func(_ context.Context, model *ai.Model, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		requestKeys = append(requestKeys, options.APIKey)
		message := runtimeTestMessage(providerID, model.ID, "branch summary text", ai.StopReasonStop)
		stream := ai.NewAssistantMessageEventStream()
		if err := stream.Push(ai.StartEvent{Partial: message}); err != nil {
			return nil, err
		}
		if err := stream.Push(ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}); err != nil {
			return nil, err
		}
		return stream, nil
	}
	if err := services.ModelRuntime().RegisterProvider(providerID, input); err != nil {
		t.Fatal(err)
	}
	model := services.ModelRuntime().GetModel(providerID, "oauth-model")
	if model == nil {
		t.Fatal("oauth-model is not registered")
	}
	created, err := CreateAgentSessionFromServices(CreateAgentSessionFromServicesOptions{Services: services, Model: model})
	if err != nil {
		t.Fatal(err)
	}
	session := created.Session
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	var target string
	for _, message := range []agent.AgentMessage{
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "first branch"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "first reply"}}}},
		{User: &agent.UserMessage{Role: agent.RoleUser, Content: ai.UserContentBlocks{ai.TextContent{Text: "abandoned branch work"}}}},
		{Assistant: &agent.AssistantMessage{Role: agent.RoleAssistant, Content: []ai.AssistantContentBlock{ai.TextContent{Text: "abandoned reply"}}}},
	} {
		id, err := session.inner.AppendMessage(message)
		if err != nil {
			t.Fatal(err)
		}
		if target == "" {
			target = id
		}
	}

	request, err := session.prepareSummarizationRequest(t.Context(), model)
	if err != nil {
		t.Fatal(err)
	}
	if request.apiKey != "" || len(request.headers) != 0 || len(request.env) != 0 {
		t.Fatalf("the summary request carries pre-resolved auth: apiKey=%q headers=%v env=%v", request.apiKey, request.headers, request.env)
	}
	result, err := session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
	if err != nil || result.SummaryEntry == nil || !strings.Contains(result.SummaryEntry.Summary, "branch summary text") {
		t.Fatalf("navigation=%#v,%v", result, err)
	}
	if len(requestKeys) != 1 || requestKeys[0] != "oauth-access" {
		t.Fatalf("provider API keys = %q, want one request with the stored OAuth access token", requestKeys)
	}
}
