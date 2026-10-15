package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

func namedModel(p *scriptedProvider) *ai.Model {
	m := scriptedModel(p)
	m.ProviderMeta.ProviderID = "acme"
	return m
}

// Pi agent-loop.ts:399-406 resolves `getApiKey(model.provider) || config.apiKey` before every request, so an expiring
// token is current on each one.
func TestAgentOptionsGetAPIKeyResolvesTheProviderCredentialPerRequest(t *testing.T) {
	provider := &scriptedProvider{id: "acme", respond: replyText("done")}
	var asked []string
	calls := 0
	a := mustNewAgent(AgentOptions{Model: namedModel(provider), GetAPIKey: func(id string) (string, error) {
		asked = append(asked, id)
		calls++
		if calls == 1 {
			return "token-1", nil
		}
		return "", nil
	}})
	mustSend(t, a, "one")
	mustSend(t, a, "two")
	if !reflect.DeepEqual(asked, []string{"acme", "acme"}) {
		t.Fatalf("getApiKey asked for %v, want the model's provider once per request", asked)
	}
	if got := provider.request(1).opts.APIKey; got != "token-1" {
		t.Fatalf("first request apiKey = %q, want token-1", got)
	}
	if got := provider.request(2).opts.APIKey; got != "" {
		t.Fatalf("second request apiKey = %q, want no override when getApiKey yields nothing", got)
	}
}

// A rejected getApiKey fails the turn through the same lifecycle as a rejected transformContext (agent.ts:519-553): the
// provider is never called.
func TestAgentOptionsGetAPIKeyRejectionFailsTheTurnBeforeTheProvider(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("never")}
	failure := errors.New("token refresh failed")
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), GetAPIKey: func(string) (string, error) { return "", failure }})
	_, _ = a.Send(t.Context(), "start")
	if n := len(provider.requests); n != 0 {
		t.Fatalf("provider saw %d requests after getApiKey rejected", n)
	}
	var last *AssistantMessage
	for _, m := range a.Messages() {
		if m.Assistant != nil {
			last = m.Assistant
		}
	}
	if last == nil || last.StopReason != ai.StopReasonError || last.ErrorMessage != failure.Error() {
		t.Fatalf("failed turn = %+v, want an error assistant message carrying %q", last, failure)
	}
}

// Pi agent.ts:473-478 forwards onPayload, onResponse and maxRetryDelayMs into every request's stream options.
func TestAgentOptionsForwardPayloadResponseAndRetryDelayHooks(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	delay := 4500
	var payloadModel, responseModel *ai.Model
	a := mustNewAgent(AgentOptions{
		Model:           scriptedModel(provider),
		MaxRetryDelayMs: &delay,
		OnPayload: func(payload any, model *ai.Model) (any, error) {
			payloadModel = model
			return payload, nil
		},
		OnResponse: func(_ context.Context, _ ai.ProviderResponse, model *ai.Model) error {
			responseModel = model
			return nil
		},
	})
	mustSend(t, a, "hi")
	opts := provider.request(1).opts
	if opts.MaxRetryDelayMs == nil || *opts.MaxRetryDelayMs != 4500 {
		t.Fatalf("maxRetryDelayMs = %v, want 4500", opts.MaxRetryDelayMs)
	}
	model := scriptedModel(provider)
	if _, err := opts.OnPayload(map[string]any{}, model); err != nil || payloadModel != model {
		t.Fatalf("onPayload not forwarded: err=%v model=%p", err, payloadModel)
	}
	if err := opts.OnResponse(t.Context(), ai.ProviderResponse{Status: 200}, model); err != nil || responseModel != model {
		t.Fatalf("onResponse not forwarded: err=%v model=%p", err, responseModel)
	}
}

// Pi agent-loop.ts:388-395 runs transformContext(messages, signal) before convertToLlm; the provider sees the result.
func TestAgentOptionsTransformContextRewritesTheProviderTranscript(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	var sawCtx context.Context
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), TransformContext: func(ctx context.Context, msgs []AgentMessage) ([]AgentMessage, error) {
		sawCtx = ctx
		return append(msgs[:len(msgs):len(msgs)], userMessage("injected")), nil
	}})
	mustSend(t, a, "hello")
	if got := userTexts(provider.request(1).transcript); !reflect.DeepEqual(got, []string{"hello", "injected"}) {
		t.Fatalf("provider saw %v", got)
	}
	if sawCtx == nil {
		t.Fatal("transformContext got no context")
	}
}

// Pi agent.ts:198 onPayload is a public mutable property that createLoopConfig (agent.ts:473) reads at the start of every run: assigning it between runs
// changes the hook the next request carries, and clearing it removes the hook. SetBeforeProviderHook is that assignment.
func TestAgentSetBeforeProviderHookReplacesTheOnPayloadHookForTheNextRun(t *testing.T) {
	provider := &scriptedProvider{respond: replyText("done")}
	var calls []string
	hook := func(name string) func(any, *ai.Model) (any, error) {
		return func(payload any, _ *ai.Model) (any, error) {
			calls = append(calls, name)
			return payload, nil
		}
	}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), OnPayload: hook("constructed")})
	model := scriptedModel(provider)

	mustSend(t, a, "one")
	if _, err := provider.request(1).opts.OnPayload(map[string]any{}, model); err != nil {
		t.Fatal(err)
	}
	a.SetBeforeProviderHook(hook("assigned"))
	mustSend(t, a, "two")
	if _, err := provider.request(2).opts.OnPayload(map[string]any{}, model); err != nil {
		t.Fatal(err)
	}
	a.SetBeforeProviderHook(nil)
	mustSend(t, a, "three")
	if provider.request(3).opts.OnPayload != nil {
		t.Fatal("a cleared onPayload still reached the next request")
	}
	if want := []string{"constructed", "assigned"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("hooks called = %v, want %v: each run uses the hook assigned before it", calls, want)
	}
}

// createLoopConfig (agent.ts:473) copies onPayload into the run's config once, so an assignment while a run is active reaches only the
// next run: the second request of a tool-call run still carries the hook the run started with.
func TestAgentSetBeforeProviderHookDuringARunReachesOnlyTheNextRun(t *testing.T) {
	provider := &scriptedProvider{}
	tag := func(name string) func(any, *ai.Model) (any, error) {
		return func(any, *ai.Model) (any, error) { return name, nil }
	}
	a := mustNewAgent(AgentOptions{Model: scriptedModel(provider), Tools: []AgentTool{noopTool()}, OnPayload: tag("first")})
	toolThenText := toolCallsThenText(toolCall("call", "noop", nil))
	provider.respond = func(call int, req scriptedRequest) *ai.AssistantMessageEventStream {
		if call == 1 {
			a.SetBeforeProviderHook(tag("second"))
		}
		return toolThenText(call, req)
	}
	mustSend(t, a, "run one")
	provider.respond = replyText("done")
	mustSend(t, a, "run two")
	var got []any
	for call := 1; call <= provider.calls(); call++ {
		payload, err := provider.request(call).opts.OnPayload(nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, payload)
	}
	if want := []any{"first", "first", "second"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hooks per request = %v, want %v: a run keeps the hook it started with", got, want)
	}
}
