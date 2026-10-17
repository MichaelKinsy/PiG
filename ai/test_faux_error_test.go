package ai

import "testing"

// test/parity/testdata/test-faux-provider.ts:emitPlan snapshots an empty pending start before publishing its classified error. Keep both sides of the paired fixture equivalent without changing real providers.
func TestTestFauxClassifiedErrorStartsPending(t *testing.T) {
	p := &TestFauxProvider{}
	stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText("")}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var events []AssistantMessageEvent
	for event := range stream.Events(t.Context()) {
		events = append(events, event)
	}
	if len(events) != 2 {
		t.Fatalf("events=%#v", events)
	}
	start, ok := events[0].(StartEvent)
	if !ok || start.Partial.Observe().StopReason != StopReasonPending || start.Partial.Observe().ErrorMessage != "" {
		t.Fatalf("start=%#v", events[0])
	}
	failure, ok := events[1].(ErrorEvent)
	if !ok || failure.Error.StopReason != StopReasonError || failure.Error.ErrorMessage != `test-faux: unhandled request ""` {
		t.Fatalf("error=%#v", events[1])
	}
}

// test-faux-provider.ts reports an unscripted request as `test-faux: unhandled request "${lastText}"`: the text sits between plain quotes, with its own quotes, backslashes and newlines unescaped.
func TestTestFauxUnhandledRequestQuotesTheTextAsTheTemplateLiteral(t *testing.T) {
	text := "<file name=\"a.txt\">\nline \\ two\n</file>\nhello"
	p := &TestFauxProvider{}
	stream, err := p.Stream(t.Context(), NormalizeContext(Context{Messages: []Message{UserMessage{Content: UserText(text)}}}), StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var message string
	for event := range stream.Events(t.Context()) {
		if failure, ok := event.(ErrorEvent); ok {
			message = failure.Error.ErrorMessage
		}
	}
	if want := "test-faux: unhandled request \"" + text + "\""; message != want {
		t.Fatalf("error message = %q, want %q", message, want)
	}
}
