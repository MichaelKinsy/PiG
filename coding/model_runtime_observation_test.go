package coding

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi's ModelRuntime and lazy forwarding preserve start while a successful response is held before its first body chunk (model-runtime.ts:617-624,638-643; lazy.ts:31-38).
func TestModelRuntimeStartBeforeBodyData(t *testing.T) {
	for _, api := range []ai.API{ai.APIOpenAICompletions, ai.APIOpenAIResponses} {
		for _, simple := range []bool{false, true} {
			for _, abort := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/simple=%t/abort=%t", api, simple, abort), func(t *testing.T) {
					release := make(chan struct{})
					unblock := sync.OnceFunc(func() { close(release) })
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						select {
						case <-release:
							if api == ai.APIOpenAICompletions {
								_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
							} else {
								_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n")
							}
						case <-r.Context().Done():
						}
					}))
					defer server.Close()
					defer unblock()
					agentDir := t.TempDir()
					config := fmt.Sprintf(`{"providers":{"observation":{"baseUrl":%q,"api":%q,"apiKey":"test","models":[{"id":"probe","name":"Probe"}]}}}`, server.URL, api)
					if err := os.WriteFile(filepath.Join(agentDir, "models.json"), []byte(config), 0o600); err != nil {
						t.Fatal(err)
					}
					services, err := NewServices(ServicesOptions{CWD: t.TempDir(), AgentDir: agentDir})
					if err != nil {
						t.Fatal(err)
					}
					model, err := BuildModel("observation/probe", services)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = model.Provider.Close() }()
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					streamFn := services.ModelRuntime().Stream
					if simple {
						streamFn = services.ModelRuntime().StreamSimple
					}
					stream := streamFn(ctx, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("probe")}}}, ai.StreamOptions{})
					observation, stop := context.WithTimeout(t.Context(), time.Second)
					defer stop()
					var first ai.AssistantMessageEvent
					for event := range stream.Events(observation) {
						first = event
						break
					}
					if abort {
						cancel()
					} else {
						unblock()
					}
					var terminal *ai.AssistantMessage
					for event := range stream.Events(t.Context()) {
						switch value := event.(type) {
						case ai.DoneEvent:
							terminal = value.Message
						case ai.ErrorEvent:
							terminal = value.Error
						}
					}
					wantReason := ai.StopReasonStop
					if abort {
						wantReason = ai.StopReasonAborted
					}
					if result := stream.Result(); result != terminal || result.StopReason != wantReason {
						t.Fatalf("result=%#v terminal=%p, want %s", result, terminal, wantReason)
					}
					start, ok := first.(ai.StartEvent)
					if !ok {
						t.Fatalf("ModelRuntime start depends on body data: first=%#v", first)
					}
					if len(start.Partial.Content) != 0 || start.Partial.StopReason != ai.StopReasonPending {
						t.Fatalf("header-only partial=%#v", start.Partial)
					}
				})
			}
		}
	}
}
