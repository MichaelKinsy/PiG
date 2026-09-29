// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-FileCopyrightText: Copyright (c) 2025 Mario Zechner
// SPDX-License-Identifier: MIT

package coding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// .upstream/v0.87.1/packages/coding-agent/test/suite/regressions/6768-copilot-compaction-base-url.test.ts:49
// Go native providers own per-model auth callbacks. Two hermetic endpoints stand
// for Pi's individual/enterprise URLs; the actual request must reach the auth URL
// through the SDK-style ModelRuntime.StreamSimple wrapper, not the catalog URL.
func TestCompactionUsesAuthResolvedBaseURLThroughRuntimeWrapper(t *testing.T) {
	var catalogCalls, enterpriseCalls atomic.Int32
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		catalogCalls.Add(1)
		http.Error(w, "wrong catalog endpoint", http.StatusBadRequest)
	}))
	defer catalog.Close()
	enterprise := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		enterpriseCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer enterprise-token" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"summary\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"summary\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11}}\n\ndata: [DONE]\n\n")
	}))
	defer enterprise.Close()
	store := ai.NewInMemoryAuthStorage(map[string]ai.Credential{"faux": {Type: ai.CredentialOAuth, Access: "enterprise-token", Refresh: "refresh-token", Expires: time.Now().Add(time.Hour).UnixMilli()}})
	auth := ai.ProviderAuth{
		APIKey: &ai.APIKeyAuth{Name: "Copilot token", Resolve: func(_ context.Context, input ai.APIKeyAuthInput) (*ai.AuthResult, error) {
			if input.Credential != nil && input.Credential.Key != "" {
				return &ai.AuthResult{Auth: ai.ModelAuth{APIKey: input.Credential.Key}, Source: "explicit token"}, nil
			}
			return nil, nil
		}},
		OAuth: &ai.OAuthAuth{Name: "Copilot OAuth", Refresh: func(_ context.Context, c ai.Credential) (ai.Credential, error) { return c, nil }, ToAuth: func(c ai.Credential) (ai.ModelAuth, error) {
			return ai.ModelAuth{APIKey: c.Access, BaseURL: enterprise.URL}, nil
		}},
	}
	resolve := func(ctx context.Context) (*ai.AuthResult, error) {
		return ai.ResolveProviderAuth(ctx, "faux", auth, store, ai.DefaultProviderAuthContext(), ai.AuthResolutionOverrides{})
	}
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "faux", Model: "faux-1", BaseURL: catalog.URL, GetAPIKey: func(ctx context.Context) (string, error) {
		result, err := resolve(ctx)
		if err != nil {
			return "", err
		}
		return result.Auth.APIKey, nil
	}, GetBaseURL: func(ctx context.Context) (string, error) {
		result, err := resolve(ctx)
		if err != nil {
			return "", err
		}
		return result.Auth.BaseURL, nil
	}})
	model := fakeModelWithProvider(provider)
	model.ID = "faux-1"
	model.ProviderMeta.BaseURL = catalog.URL
	model.ProviderMeta.API = "openai-completions"
	model.Capabilities.ContextWindow = 200000
	s, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UnixMilli()
	persistQueueMessage(t, s, queueUser("message to compact", now-1000))
	message := queueAssistant(s, "assistant response to compact", 100, 0, ai.StopReasonStop, now-500)
	message.API = "openai-completions"
	persistQueueMessage(t, s, agent.AgentMessage{Assistant: message})
	s.RefreshContext()
	runtime := s.services.ModelRuntime()
	s.streamFn = func(ctx context.Context, requestModel *ai.Model, system string, messages []agent.AgentMessage, options ai.StreamOptions) (string, *ai.Usage, error) {
		stream := runtime.StreamSimple(ctx, requestModel, ai.Context{SystemPrompt: system, Messages: agent.ConvertToLLM(messages, requestModel)}, options)
		response := stream.Result()
		if response.StopReason == ai.StopReasonError {
			return "", nil, fmt.Errorf("%s", response.ErrorMessage)
		}
		var text string
		for _, block := range response.Content {
			if block, ok := block.(ai.TextContent); ok {
				text += block.Text
			}
		}
		return text, &response.Usage, nil
	}
	if err := s.Compact(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	if catalogCalls.Load() != 0 || enterpriseCalls.Load() == 0 {
		t.Fatalf("catalog requests=%d auth-resolved requests=%d", catalogCalls.Load(), enterpriseCalls.Load())
	}
}
