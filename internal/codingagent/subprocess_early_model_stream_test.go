package codingagent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

type earlyStreamBridge struct {
	stream func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error)
}

func (b *earlyStreamBridge) SetHostAction(key string, fn any) {
	if key == "streamModel" {
		b.stream = fn.(func(context.Context, map[string]any, map[string]any) (*ai.AssistantMessageEventStream, error))
	}
}
func (*earlyStreamBridge) PublishModelCatalog() {}

// Pi runs `completeSimple(model, context, { apiKey })` in the extension's own process, with no Session: a module may call it before the Host has bound one.
// The model is the caller's object and the request goes through the global API provider; a model named only by provider and id still needs the Session.
func TestEarlyModelStreamServesAPICallsWithoutASession(t *testing.T) {
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, `data: {"id":"c","object":"chat.completion.chunk","created":0,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	bridge := &earlyStreamBridge{}
	WireEarlyModelStream(bridge, "")
	if bridge.stream == nil {
		t.Fatal("WireEarlyModelStream did not install a streamModel action")
	}
	model := map[string]any{"id": "m", "name": "m", "provider": "probe-provider", "api": ai.APIOpenAICompletions, "baseUrl": server.URL + "/v1", "reasoning": false, "contextWindow": 1000, "maxTokens": 100, "input": []string{"text"}}
	request := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "ping", "timestamp": 1}}, "apiKey": "k"}

	apiCtx := extension.WithModelStreamRequest(t.Context(), extension.ModelStreamRequest{API: true})
	stream, err := bridge.stream(apiCtx, model, request)
	if err != nil {
		t.Fatal(err)
	}
	for range stream.Events(apiCtx) {
	}
	result := stream.Result()
	if result.StopReason != ai.StopReasonStop || len(result.Content) != 1 || result.Content[0].(ai.TextContent).Text != "pong" {
		t.Fatalf("result = %+v", result)
	}
	if gotKey != "Bearer k" {
		t.Fatalf("Authorization = %q, want the module's apiKey", gotKey)
	}

	_, err = bridge.stream(t.Context(), map[string]any{"provider": "p", "modelId": "m"}, request)
	if err == nil || !strings.Contains(err.Error(), "session model runtime not available") {
		t.Fatalf("a model named by provider and id without a Session: err = %v", err)
	}
}
