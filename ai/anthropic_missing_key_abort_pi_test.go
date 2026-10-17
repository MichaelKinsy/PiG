//go:build !pig_strip_mistral_conversations && !pig_strip_openai_codex_responses

package ai

import (
	"context"
	"testing"
)

// anthropic-messages.ts:611-613 asserts the request auth inside stream()'s own try block, whose catch sets `output.stopReason = options?.signal?.aborted ? "aborted" : "error"` (:893),
// while streamSimple (:933-935) asserts before the stream starts, so lazy.ts createSetupErrorMessage reports a setup failure whose stop reason is always "error".
// With no key and a signal that is already aborted the two forms therefore end differently, and both carry `No API key for provider: <provider>` without sending a request.
func TestAnthropicMessagesAPIMissingKeyIsAbortAwareForStreamAndASetupFailureForStreamSimple(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_OAUTH_TOKEN"} {
		t.Setenv(key, "")
	}
	for _, tc := range []struct {
		form      string
		stream    APIStreamFunction
		cancelled bool
		want      StopReason
	}{
		{"stream", AnthropicMessagesAPI().Stream, false, StopReasonError},
		{"stream", AnthropicMessagesAPI().Stream, true, StopReasonAborted},
		{"streamSimple", AnthropicMessagesAPI().StreamSimple, false, StopReasonError},
		{"streamSimple", AnthropicMessagesAPI().StreamSimple, true, StopReasonError},
	} {
		srv, seen := recordingEndpoint(t)
		model := compatTestModel("anthropic", APIAnthropicMessages)
		model.ProviderMeta.BaseURL = srv.URL
		ctx, cancel := context.WithCancel(t.Context())
		if tc.cancelled {
			cancel()
		}
		events, err := tc.stream(ctx, model, NormalizeContext(compatHello), StreamOptions{})
		if err != nil || events == nil {
			t.Fatalf("%s cancelled=%v: a missing key failed the call: %v", tc.form, tc.cancelled, err)
		}
		message := events.Result()
		cancel()
		if message.StopReason != tc.want || message.ErrorMessage != "No API key for provider: anthropic" {
			t.Errorf("%s cancelled=%v: result = %v %q, want %v %q", tc.form, tc.cancelled, message.StopReason, message.ErrorMessage, tc.want, "No API key for provider: anthropic")
		}
		if got := seen(); got.path != "" {
			t.Errorf("%s cancelled=%v: a missing key still sent a request to %q", tc.form, tc.cancelled, got.path)
		}
	}
}

// anthropic-messages.ts:614-615 and 932-937: both forms skip the request-auth assertion when workload identity federation is configured, so a call with no key and
// no auth header streams the answer through the federated token instead of failing with the missing-key error.
func TestAnthropicMessagesAPIWithoutAKeyFederatesInBothForms(t *testing.T) {
	for name, value := range federationEnvFor(t) {
		t.Setenv(name, value)
	}
	for form, stream := range map[string]APIStreamFunction{"stream": AnthropicMessagesAPI().Stream, "streamSimple": AnthropicMessagesAPI().StreamSimple} {
		wire := &federationWire{}
		model := compatTestModel("anthropic", APIAnthropicMessages)
		model.ProviderMeta.BaseURL = wire.serve(t)
		events, err := stream(t.Context(), model, NormalizeContext(federationContext), StreamOptions{})
		if err != nil || events == nil {
			t.Fatalf("%s = %v, %v", form, events, err)
		}
		message := events.Result()
		if message.StopReason != StopReasonStop {
			t.Fatalf("%s: stop reason = %q, error = %q, want the federated answer", form, message.StopReason, message.ErrorMessage)
		}
		if got := len(wire.byPath("/v1/oauth/token")); got != 1 {
			t.Errorf("%s: token exchanges = %d, want 1", form, got)
		}
		requests := wire.byPath("/v1/messages")
		if len(requests) == 0 {
			t.Fatalf("%s: no message request reached the endpoint", form)
		}
		for _, request := range requests {
			if got := request.header.Get("Authorization"); got != "Bearer federated-token" {
				t.Errorf("%s: message Authorization = %q, want %q", form, got, "Bearer federated-token")
			}
		}
	}
}

// provider-env.ts:45-51 getProviderEnvValue reads options.env before process.env, and both forms pass options?.env to getAnthropicFederation (anthropic-messages.ts:612,932),
// so federation configured only through StreamOptions.Env, with the process variables empty, still skips the missing-key assertion and streams through the federated token.
// mutation-checked: reading federation from the process env instead of options.Env (directSimpleAPIKey) fails the streamSimple form.
func TestAnthropicMessagesAPIFederatesFromTheOptionsEnvInBothForms(t *testing.T) {
	env := federationEnvFor(t)
	for form, stream := range map[string]APIStreamFunction{"stream": AnthropicMessagesAPI().Stream, "streamSimple": AnthropicMessagesAPI().StreamSimple} {
		wire := &federationWire{}
		model := compatTestModel("anthropic", APIAnthropicMessages)
		model.ProviderMeta.BaseURL = wire.serve(t)
		events, err := stream(t.Context(), model, NormalizeContext(federationContext), StreamOptions{Env: env})
		if err != nil || events == nil {
			t.Fatalf("%s = %v, %v", form, events, err)
		}
		if message := events.Result(); message.StopReason != StopReasonStop {
			t.Fatalf("%s: stop reason = %q, error = %q, want the federated answer", form, message.StopReason, message.ErrorMessage)
		}
		requests := wire.byPath("/v1/messages")
		if len(requests) == 0 {
			t.Fatalf("%s: no message request reached the endpoint", form)
		}
		for _, request := range requests {
			if got := request.header.Get("Authorization"); got != "Bearer federated-token" {
				t.Errorf("%s: message Authorization = %q, want %q", form, got, "Bearer federated-token")
			}
		}
	}
}
