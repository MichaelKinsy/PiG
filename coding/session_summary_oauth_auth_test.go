package coding

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Pi 6fed3cfb (#11520): packages/coding-agent/test/suite/regressions/11520-sdk-summary-oauth-auth.test.ts.
// Pi's fix keeps a Session's summaries from passing the stored OAuth token back to the runtime as an explicit API key, which would take the API-key override branch of resolveProviderAuth.
// A Go Session already does not: a runtime-bound catalog model is neither a registry-built provider nor a native-auth provider, so prepareSummarizationRequest resolves nothing and the request carries no key; the stream resolves the stored OAuth login once, as a prompt does.
// Registry-built providers (the only ones whose summary auth is pre-resolved) exist only for providers without a native registration, which have no OAuth method.
func TestSDKSummaryLetsTheStreamResolveStoredOAuthUpstream(t *testing.T) {
	var mu sync.Mutex
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authorizations = append(authorizations, r.Header.Get("Authorization"))
		mu.Unlock()
		http.Error(w, "refused", http.StatusBadRequest)
	}))
	defer server.Close()
	const token = "sk-ant-oat01-oauth-access"
	services, _ := nativeCompatServices(t, fmt.Sprintf(`{"providers":{"anthropic":{"baseUrl":%q}}}`, server.URL), map[string]ai.Credential{
		"anthropic": {Type: ai.CredentialOAuth, Access: token, Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()},
	})
	models := services.ModelRuntime().GetModels("anthropic")
	if len(models) == 0 {
		t.Fatal("anthropic catalog is empty")
	}
	session, err := NewSession(services, SessionOptions{Model: models[0], NoSession: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	}()
	request, err := session.prepareSummarizationRequest(t.Context(), models[0])
	if err != nil || request.apiKey != "" {
		t.Fatalf("summary request auth = %q, %v; the stream must resolve the stored OAuth login itself", request.apiKey, err)
	}
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
	// The endpoint refuses the request; the summary's auth is what this test observes.
	_, _ = session.NavigateTree(t.Context(), target, NavigateTreeOptions{Summarize: true})
	mu.Lock()
	defer mu.Unlock()
	if len(authorizations) == 0 || authorizations[0] != "Bearer "+token {
		t.Fatalf("the summary request carried %q, want the stored OAuth token", authorizations)
	}
}
