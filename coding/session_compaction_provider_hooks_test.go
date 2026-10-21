package coding

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// hookProbeProvider acts as a provider adapter does: it applies the request's payload, response and stream-event callbacks when the request carries them, and records the headers and payload it is sent.
type hookProbeProvider struct {
	mu       sync.Mutex
	headers  []ai.ProviderHeaders
	payloads []any
}

func (p *hookProbeProvider) ID() string   { return "faux" }
func (p *hookProbeProvider) Close() error { return nil }
func (p *hookProbeProvider) Stream(ctx context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	model := &ai.Model{ID: "faux-1", Provider: p}
	var payload any = map[string]any{"model": "faux-1", "max_tokens": float64(10)}
	if options.OnPayload != nil {
		replaced, err := options.OnPayload(payload, model)
		if err != nil {
			return nil, err
		}
		if replaced != nil {
			payload = replaced
		}
	}
	if options.OnResponse != nil {
		if err := options.OnResponse(ctx, ai.ProviderResponse{Status: 200, Headers: map[string]string{"x-probe": "1"}}, model); err != nil {
			return nil, err
		}
	}
	if options.OnProviderStreamEvent != nil {
		if err := options.OnProviderStreamEvent(ctx, map[string]any{"type": "probe"}, model); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	p.headers = append(p.headers, options.Headers)
	p.payloads = append(p.payloads, payload)
	p.mu.Unlock()
	message := fauxReply("summary text", ai.StopReasonStop, 0)(nil)
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

var providerHookEvents = []string{"before_provider_headers", "before_provider_request", "after_provider_response", "provider_stream_event"}

// providerHookSession builds a Session whose extension logs the four provider events and rewrites the payload and headers it sees.
func providerHookSession(t *testing.T) (*Session, *hookProbeProvider, func() []string) {
	t.Helper()
	probe := &hookProbeProvider{}
	provider := newRequestOptionsProvider(probe, "opencode", nil)
	var mu sync.Mutex
	var log []string
	record := func(name string) { mu.Lock(); log = append(log, name); mu.Unlock() }
	handlers := map[string][]extension.HandlerFn{
		"before_provider_headers": {func(args ...any) (any, error) {
			record("before_provider_headers")
			args[0].(extension.BeforeProviderHeadersEvent).Headers["x-hook"] = new("seen")
			return nil, nil
		}},
		"before_provider_request": {func(args ...any) (any, error) {
			record("before_provider_request")
			payload := args[0].(extension.BeforeProviderRequestEvent).Payload.(map[string]any)
			return map[string]any{"model": payload["model"], "max_tokens": float64(123)}, nil
		}},
		"after_provider_response": {func(...any) (any, error) { record("after_provider_response"); return nil, nil }},
		"provider_stream_event":   {func(...any) (any, error) { record("provider_stream_event"); return nil, nil }},
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/provider-hooks", Handlers: handlers}}, t.TempDir())
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: provider, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	sess, err := NewSession(newTestServicesSmallKeep(t), SessionOptions{Model: model, SkipBuiltinTools: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	drainSessionEvents(t, sess)
	return sess, probe, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(log)
	}
}

func seedProviderHookSession(t *testing.T, sess *Session) {
	t.Helper()
	for range 3 {
		if _, err := sess.Send(context.Background(), "a question long enough to be kept apart from the next one"); err != nil {
			t.Fatal(err)
		}
	}
	drainSessionEvents(t, sess)
}

// An agent request carries all four provider events in order: headers, payload, response, stream event (sdk.ts streamFn + createAgent options).
func TestAgentRequestEmitsAllProviderEvents(t *testing.T) {
	sess, probe, log := providerHookSession(t)
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if got := log(); !slices.Equal(got, providerHookEvents) {
		t.Fatalf("events for an agent request = %v, want %v", got, providerHookEvents)
	}
	if got := probe.payloads[0].(map[string]any)["max_tokens"]; got != float64(123) {
		t.Fatalf("agent payload max_tokens = %v, want the handler's rewrite", got)
	}
}

// Compaction and branch summaries send through the Agent's stream function, as agent-session.ts passes this.agent.streamFunction to compact and generateBranchSummary. That function only adds before_provider_headers (sdk.ts buildRequestOptions); before_provider_request, after_provider_response and provider_stream_event are Agent options (sdk.ts onPayload/onResponse/onProviderStreamEvent) that summarization requests do not carry (compaction.ts createSummarizationOptions).
func TestSummarizationRequestsEmitOnlyProviderHeaders(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, sess *Session)
	}{
		{"manual", func(t *testing.T, sess *Session) {
			if _, err := sess.Compact(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"threshold", func(t *testing.T, sess *Session) {
			if ok, err := sess.runAutoCompaction(context.Background(), "threshold", false); err != nil || !ok {
				t.Fatalf("auto compaction = %v, %v", ok, err)
			}
		}},
		{"overflow", func(t *testing.T, sess *Session) {
			if ok, err := sess.runAutoCompaction(context.Background(), "overflow", true); err != nil || !ok {
				t.Fatalf("overflow compaction = %v, %v", ok, err)
			}
		}},
		{"branch-summary", func(t *testing.T, sess *Session) {
			target := sess.inner.GetEntries()[0].Base().ID
			result, err := sess.NavigateTree(context.Background(), target, NavigateTreeOptions{Summarize: true})
			if err != nil || result.Cancelled || result.SummaryEntry == nil {
				t.Fatalf("navigate = %+v, %v", result, err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sess, probe, log := providerHookSession(t)
			seedProviderHookSession(t, sess)
			before, requests := len(log()), len(probe.payloads)
			test.run(t, sess)
			got := log()[before:]
			sent := len(probe.payloads) - requests
			if sent == 0 {
				t.Fatal("no summary request reached the provider")
			}
			want := slices.Repeat([]string{"before_provider_headers"}, sent)
			if !slices.Equal(got, want) {
				t.Fatalf("events for %d %s summary requests = %v, want %v", sent, test.name, got, want)
			}
			if headers := probe.headers[requests]["x-hook"]; headers == nil || *headers != "seen" {
				t.Fatalf("summary request headers = %v, want the handler's x-hook", probe.headers[requests])
			}
			if got := probe.payloads[requests].(map[string]any)["max_tokens"]; got != float64(10) {
				t.Fatalf("summary payload max_tokens = %v, want the untouched 10: Pi's summaries carry no onPayload", got)
			}
		})
	}
}
