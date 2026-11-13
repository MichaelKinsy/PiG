package coding

// pi: packages/coding-agent/src/core/sdk.ts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

type hookTrace struct {
	mu     sync.Mutex
	events []string
}

func (h *hookTrace) add(e string) {
	h.mu.Lock()
	h.events = append(h.events, e)
	h.mu.Unlock()
}

func (h *hookTrace) snapshot() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.events)
}

// tracingProvider runs the request hooks in a provider's order (payload, response, stream event) and records when it sends.
type tracingProvider struct{ trace *hookTrace }

func (p *tracingProvider) ID() string   { return "faux" }
func (p *tracingProvider) Close() error { return nil }
func (p *tracingProvider) Stream(ctx context.Context, _ ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	var payload any = map[string]any{"max_tokens": float64(10)}
	if options.OnPayload != nil {
		replaced, err := options.OnPayload(payload, nil)
		if err != nil {
			return nil, err
		}
		if replaced != nil {
			payload = replaced
		}
	}
	p.trace.add("send:" + payloadMaxTokens(payload))
	if options.OnResponse != nil {
		if err := options.OnResponse(ctx, ai.ProviderResponse{Status: 200}, nil); err != nil {
			return nil, err
		}
	}
	p.trace.add("response-consumed")
	if options.OnProviderStreamEvent != nil {
		if err := options.OnProviderStreamEvent(ctx, map[string]any{"n": 1}, &ai.Model{ID: "m"}); err != nil {
			return nil, err
		}
	}
	p.trace.add("event-consumed")
	message := fauxReply("ok", ai.StopReasonStop, 0)(nil)
	return newSessionTestStream(ai.StartEvent{Partial: message}, ai.DoneEvent{Reason: ai.StopReasonStop, Message: message}), nil
}

func payloadMaxTokens(payload any) string {
	if v, ok := payload.(map[string]any)["max_tokens"].(float64); ok && v == 123 {
		return "123"
	}
	return "10"
}

func tracingSession(t *testing.T, trace *hookTrace, runner *inproc.Runner) *Session {
	t.Helper()
	model := &ai.Model{ID: "faux-1", DisplayName: "faux-1", Provider: &tracingProvider{trace: trace}, Capabilities: ai.ModelCapabilities{ContextWindow: 128_000}}
	sess, err := NewSession(newTestServices(t), SessionOptions{Model: model, SkipBuiltinTools: true, NoSession: true, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	drainSessionEvents(t, sess)
	return sess
}

func allHookExtension(trace *hookTrace) extension.Extension {
	return extension.Extension{Path: "/ext/all", Handlers: map[string][]extension.HandlerFn{
		"context": {func(...any) (any, error) { trace.add("context"); return nil, nil }},
		"before_provider_request": {func(args ...any) (any, error) {
			trace.add("payload-hook")
			return map[string]any{"max_tokens": float64(123)}, nil
		}},
		"after_provider_response": {func(...any) (any, error) { trace.add("response-hook"); return nil, nil }},
		"provider_stream_event":   {func(...any) (any, error) { trace.add("event-hook"); return nil, nil }},
	}}
}

// sdk.ts:374-433: one request runs transformContext, then onPayload (its rewrite is what is sent), then onResponse and onProviderStreamEvent, each
// completed before the provider goes on.
func TestSDKRequestHooksRunInRequestOrderAndEachCompletesBeforeTheProviderContinues(t *testing.T) {
	trace := &hookTrace{}
	sess := tracingSession(t, trace, inproc.NewRunner([]extension.Extension{allHookExtension(trace)}, t.TempDir()))
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	want := []string{"context", "payload-hook", "send:123", "response-hook", "response-consumed", "event-hook", "event-consumed"}
	if got := trace.snapshot(); !slices.Equal(got, want) {
		t.Fatalf("trace = %q, want %q", got, want)
	}
}

// sdk.ts:341-343, :374-433: every hook reads extensionRunnerRef at call time, so a handler subscribed after the Agent was built, and a runner that
// replaces the first one, are honoured on the next request.
func TestSDKRequestHooksFollowALateBoundRunner(t *testing.T) {
	trace := &hookTrace{}
	late := extension.Extension{Path: "/ext/late", Handlers: map[string][]extension.HandlerFn{}}
	late.InitializeEventHandlers()
	sess := tracingSession(t, trace, inproc.NewRunner([]extension.Extension{late}, t.TempDir()))
	if _, err := sess.Send(context.Background(), "first"); err != nil {
		t.Fatal(err)
	}
	if got := trace.snapshot(); !slices.Equal(got, []string{"send:10", "response-consumed", "event-consumed"}) {
		t.Fatalf("before any handler: trace = %q", got)
	}
	late.AddEventHandler("context", 1, func(...any) (any, error) { trace.add("context"); return nil, nil })
	late.AddEventHandler("before_provider_request", 2, func(...any) (any, error) {
		trace.add("payload-hook")
		return map[string]any{"max_tokens": float64(123)}, nil
	})
	if _, err := sess.Send(context.Background(), "second"); err != nil {
		t.Fatal(err)
	}
	second := trace.snapshot()[3:]
	if !slices.Equal(second, []string{"context", "payload-hook", "send:123", "response-consumed", "event-consumed"}) {
		t.Fatalf("with late handlers: trace = %q", second)
	}
	sess.ReplaceRunner(inproc.NewRunner([]extension.Extension{{Path: "/ext/new", Handlers: map[string][]extension.HandlerFn{
		"context": {func(...any) (any, error) { trace.add("context-new"); return nil, nil }},
	}}}, t.TempDir()))
	if _, err := sess.Send(context.Background(), "third"); err != nil {
		t.Fatal(err)
	}
	if third := trace.snapshot()[8:]; !slices.Equal(third, []string{"context-new", "send:10", "response-consumed", "event-consumed"}) {
		t.Fatalf("after the runner was replaced: trace = %q", third)
	}
}

// sdk.ts:374-409, :428-433 await the emit: a hook call does not return while a handler is running, and a handler's failure is reported to the
// runner's error listeners without failing the request (runner.ts emit catches and continues).
func TestSDKProviderHooksAreAwaitedAndReportHandlerErrors(t *testing.T) {
	var gate chan struct{}
	entered := make(chan struct{}, 1)
	blocking := func(...any) (any, error) {
		entered <- struct{}{}
		<-gate
		return nil, nil
	}
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/block", Handlers: map[string][]extension.HandlerFn{
		"context":                 {blocking},
		"before_provider_request": {func(...any) (any, error) { entered <- struct{}{}; <-gate; return map[string]any{"x": 1}, nil }},
		"after_provider_response": {blocking},
		"provider_stream_event":   {blocking},
	}}}, t.TempDir())
	sess := tracingSession(t, &hookTrace{}, runner)
	calls := map[string]func() error{
		"context": func() error {
			_, err := sess.extensionContextHook(t.Context(), nil)
			return err
		},
		"payload": func() error {
			out, err := sess.extensionProviderRequestHook(map[string]any{}, nil)
			if err == nil && fmt.Sprint(out.(map[string]any)["x"]) != "1" {
				return errors.New("the payload handler's replacement was not returned")
			}
			return err
		},
		"response": func() error {
			return sess.extensionProviderResponseHook(t.Context(), ai.ProviderResponse{Status: 200}, nil)
		},
		"event": func() error {
			return sess.extensionProviderStreamEventHook(t.Context(), map[string]any{}, &ai.Model{ID: "m"})
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				gate = make(chan struct{})
				closeGate := sync.OnceFunc(func() { close(gate) })
				defer closeGate()
				entered = make(chan struct{}, 1)
				done := make(chan error, 1)
				go func() { done <- call() }()
				select {
				case <-entered:
				case err := <-done:
					t.Fatalf("the hook returned (%v) without running its handler", err)
				}
				synctest.Wait()
				select {
				case err := <-done:
					t.Fatalf("the hook returned (%v) while its handler was still running", err)
				default:
				}
				closeGate()
				if err := <-done; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestSDKProviderHookHandlerErrorsAreReportedAndDoNotFailTheRequest(t *testing.T) {
	trace := &hookTrace{}
	fail := func(...any) (any, error) { return nil, errors.New("observer failed") }
	runner := inproc.NewRunner([]extension.Extension{{Path: "/ext/fail", Handlers: map[string][]extension.HandlerFn{
		"after_provider_response": {fail},
		"provider_stream_event":   {fail},
	}}}, t.TempDir())
	var mu sync.Mutex
	var reported []string
	runner.AddErrorListener(func(err *extension.ExtensionError) { mu.Lock(); reported = append(reported, err.Error); mu.Unlock() })
	sess := tracingSession(t, trace, runner)
	if _, err := sess.Send(context.Background(), "go"); err != nil {
		t.Fatalf("a failing observer failed the request: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(reported, []string{"observer failed", "observer failed"}) {
		t.Fatalf("reported = %q, want one report per failing observer", reported)
	}
	if got := trace.snapshot(); got[len(got)-1] != "event-consumed" {
		t.Fatalf("trace = %q: the request did not continue", got)
	}
}
