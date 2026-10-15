package ai

import (
	"reflect"
	"testing"
)

// packages/ai/test/faux-provider.test.ts:138 "can replace and append queued responses", driven the way Pi drives it: through the registration registerFauxProvider
// returns, whose model streams through the global API registry. setResponses replaces the queue, appendResponses adds to its end, getPendingResponseCount counts what
// is queued, and each completion takes the front of the queue.
// mutation-checked: making FauxProviderHandle.AppendResponses a no-op, or PendingResponseCount return 0, fails it.
func TestFauxProviderRegistrationQueueMethodsUpstream(t *testing.T) {
	var registration *FauxProviderRegistration = RegisterFauxProvider()
	t.Cleanup(registration.Unregister)
	complete := func() *AssistantMessage {
		t.Helper()
		result, err := Complete(t.Context(), registration.GetModel(), Context{Messages: []Message{UserMessage{Content: UserText("hi")}}})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	text := func(want string) []AssistantContentBlock { return []AssistantContentBlock{TextContent{Text: want}} }

	registration.SetResponses([]FauxResponseStep{fauxUpstreamText("first")})
	if got := complete().Content; !reflect.DeepEqual(got, text("first")) || registration.PendingResponseCount() != 0 {
		t.Fatalf("first = %#v, pending %d", got, registration.PendingResponseCount())
	}
	registration.SetResponses([]FauxResponseStep{fauxUpstreamText("second")})
	if registration.PendingResponseCount() != 1 {
		t.Fatalf("pending after SetResponses = %d, want 1", registration.PendingResponseCount())
	}
	if got := complete().Content; !reflect.DeepEqual(got, text("second")) {
		t.Fatalf("second = %#v", got)
	}
	registration.AppendResponses([]FauxResponseStep{fauxUpstreamText("third"), fauxUpstreamText("fourth")})
	if registration.PendingResponseCount() != 2 {
		t.Fatalf("pending after AppendResponses = %d, want 2", registration.PendingResponseCount())
	}
	for _, want := range []string{"third", "fourth"} {
		if got := complete().Content; !reflect.DeepEqual(got, text(want)) {
			t.Fatalf("%s = %#v", want, got)
		}
	}
	if registration.PendingResponseCount() != 0 {
		t.Fatalf("pending at the end = %d, want 0", registration.PendingResponseCount())
	}
	// setResponses replaces what is queued instead of adding to it.
	registration.SetResponses([]FauxResponseStep{fauxUpstreamText("a"), fauxUpstreamText("b")})
	registration.SetResponses([]FauxResponseStep{fauxUpstreamText("c")})
	if registration.PendingResponseCount() != 1 {
		t.Fatalf("pending after a second SetResponses = %d, want 1", registration.PendingResponseCount())
	}
}
