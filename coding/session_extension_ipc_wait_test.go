package coding

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// Complete path: Session -> Agent listener -> extension runner -> subprocess bridge -> a real Node handler that awaits a timer. Pi's in-process handler awaits while the provider keeps running, so the first subscriber to see message_start afterwards observes the content the provider streamed during the wait, with the stop reason pinned at the Agent's copy (agent-loop.ts:408-433).
func TestSessionNodeExtensionAwaitLetsProviderAdvance(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	// The first Load materializes the Node runtime (about 1,800 files) into this test's own PIG_HOME, so the bound must cover a cold build on a loaded Windows runner as well as the run.
	ctx := testbudget.Context(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"final\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{BaseURL: server.URL, APIKey: "test", Model: "probe", ProviderID: "probe-provider"})
	defer func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	}()
	source, err := filepath.Abs(filepath.Join("testdata", "d82-w7", "message-start-wait-extension.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	host := subprocess.NewHost(t.TempDir())
	t.Cleanup(func() { host.Shutdown("Session complete") })
	loaded, err := host.Load(ctx, subprocess.ExtConfig{Name: "message-start-wait-extension", Source: source, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	h := newRecoveryHarness(t, harnessOptions{extension: *loaded})
	// The Agent's run context carries the continuation queue the provider must share with its consumer, as in the production Model Runtime path.
	h.session.Agent().SetStreamFunction(func(runContext context.Context, _ *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		return provider.Stream(runContext, transcript, options)
	})
	var subscriber []byte
	unsubscribe := h.session.Subscribe(func(event agent.AgentEvent) {
		if start, ok := event.(agent.MessageStartEvent); ok && start.Message.Assistant != nil {
			subscriber, _ = json.Marshal(start.Message)
		}
	})
	defer unsubscribe()
	if _, err := h.session.Send(ctx, "probe"); err != nil {
		t.Fatal(err)
	}
	var observed agent.AgentMessage
	if err := json.Unmarshal(subscriber, &observed); err != nil {
		t.Fatalf("no assistant message_start reached the subscriber: %v", err)
	}
	if observed.Assistant.StopReason != ai.StopReasonPending || len(observed.Assistant.Content) != 1 {
		t.Fatalf("subscriber saw %s; want the content streamed during the extension's await with the pending stop reason", subscriber)
	}
}
