package coding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// agent-session.ts:894-919 awaits _emitExtensionEvent, and runner.ts:emit awaits every handler while the provider keeps running. The context an extension handler receives must therefore carry the event's stream scope, so a handler (or the subprocess bridge acting for it) can wait without freezing the provider. The observation the subscriber sees afterwards includes the provider's progress during that wait.
func TestSessionExtensionHandlerContextCarriesStreamScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	release := make(chan struct{})
	releaseBody := sync.OnceFunc(func() { close(release) })
	defer releaseBody()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"final\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	var stream *ai.AssistantMessageEventStream
	var scoped, resumed bool
	var duringWait, afterWait []byte
	ext := extension.Extension{Path: "scoped-wait", Handlers: map[string][]extension.HandlerFn{
		"message_start": {func(args ...any) (any, error) {
			message := args[0].(extension.MessageStartEvent).Message
			if message.Assistant == nil {
				return nil, nil
			}
			observation := ai.StreamObservationFromContext(args[1].(context.Context))
			scoped = observation != nil
			if !scoped {
				return nil, fmt.Errorf("handler context has no stream scope")
			}
			var err error
			duringWait, err = json.Marshal(message)
			if err != nil {
				return nil, err
			}
			releaseBody()
			// The wait is the extension process's reply. It ends only after the provider consumed the body it was held on.
			if err := observation.AwaitExternal(func() error { stream.Result(); return nil }); err != nil {
				return nil, err
			}
			resumed = true
			afterWait, err = json.Marshal(message)
			return nil, err
		}},
	}}
	h := newRecoveryHarness(t, harnessOptions{extension: ext})
	h.session.Agent().SetStreamFunction(func(_ context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		var err error
		stream, err = provider.Stream(ctx, transcript, options)
		return stream, err
	})
	var subscriber []byte
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.MessageStartEvent); ok && start.Message.Assistant != nil {
			if !resumed {
				t.Error("subscriber overtook the extension's awaited handler")
			}
			subscriber, _ = json.Marshal(start.Message)
		}
	})
	defer unsubscribe()
	if _, err := h.session.Send(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	if !scoped || !resumed {
		t.Fatalf("scoped=%v resumed=%v", scoped, resumed)
	}
	var before, after agent.AgentMessage
	if err := json.Unmarshal(duringWait, &before); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(afterWait, &after); err != nil {
		t.Fatal(err)
	}
	if len(before.Assistant.Content) != 0 || before.Assistant.StopReason != ai.StopReasonPending {
		t.Errorf("message before the wait = %s; want empty pending", duringWait)
	}
	if len(after.Assistant.Content) != 1 || after.Assistant.StopReason != ai.StopReasonPending {
		t.Errorf("message after the wait = %s; want the provider's content with the copied pending stop reason", afterWait)
	}
	if string(afterWait) != string(subscriber) {
		t.Errorf("extension and subscriber observations differ: %s / %s", afterWait, subscriber)
	}
}
