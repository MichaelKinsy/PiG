package ai

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// errorKeyFirst reports whether the encoded message lists "errorMessage" before "timestamp".
func errorKeyFirst(t *testing.T, message AssistantMessage) bool {
	t.Helper()
	data, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	e, ts := strings.Index(string(data), `"errorMessage"`), strings.Index(string(data), `"timestamp"`)
	if e < 0 || ts < 0 {
		t.Fatalf("no errorMessage or timestamp in %s", data)
	}
	return e < ts
}

// Pi's key order depends on where the message was built: faux.ts createErrorMessage and fauxAssistantMessage write errorMessage
// before timestamp (faux.ts:97-99, :308-317), a provider's catch block sets errorMessage on the output it built with its
// timestamp. The message records the order it was built in and every encoder writes it.
// mutation-checked: a faux site that does not set ErrorBeforeTimestamp, or an encoder that ignores it, fails the site's row.
func TestAssistantErrorMessageKeyOrderFollowsTheConstructionSite(t *testing.T) {
	request, options := fauxCoreRequest()
	stream := func(steps ...FauxResponseStep) *AssistantMessage {
		core := CreateFauxCore(RegisterFauxProviderOptions{})
		core.SetResponses(steps)
		s, err := core.Stream(t.Context(), core.GetModel(), request, options)
		if err != nil {
			t.Fatal(err)
		}
		return s.Result()
	}
	for name, tc := range map[string]struct {
		message func() AssistantMessage
		first   bool
	}{
		"faux createErrorMessage (an empty queue)": {func() AssistantMessage { return *stream() }, true},
		"faux scripted error response": {func() AssistantMessage {
			return *stream(FauxStaticStep(FauxResponse{StopReason: "error", ErrorMessage: "503 service unavailable"}))
		}, true},
		"fauxAssistantMessage with errorMessage": {func() AssistantMessage {
			return FauxAssistantMessage(FauxText("x"), FauxAssistantMessageOptions{StopReason: "error", ErrorMessage: "boom"})
		}, true},
		"a provider's catch block": {func() AssistantMessage {
			builder := newObservedProviderBuilder(t.Context(), "api", "provider", "model")
			builder.fail(StopReasonError, errString("boom"))
			return *builder.stream.Result()
		}, false},
		"faux abort of a partial without errorMessage": {func() AssistantMessage {
			builder := newObservedProviderBuilder(t.Context(), "api", "faux", "model")
			abortFauxStream(builder)
			return *builder.stream.Result()
		}, false},
		"faux abort of a partial that holds errorMessage": {func() AssistantMessage {
			builder := newObservedProviderBuilder(t.Context(), "api", "faux", "model")
			builder.partial.ErrorMessage = "scripted"
			builder.partial.ErrorBeforeTimestamp = true
			abortFauxStream(builder)
			return *builder.stream.Result()
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			message := tc.message()
			if got := errorKeyFirst(t, message); got != tc.first {
				t.Fatalf("errorMessage before timestamp = %v, want %v", got, tc.first)
			}
			// Decoding keeps the order a stored message lists, and encoding the decoded message writes it again.
			data, _ := json.Marshal(message)
			var decoded AssistantMessage
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			again, _ := json.Marshal(decoded)
			if !bytes.Equal(again, data) {
				t.Fatalf("round trip changed the record:\n got %s\nwant %s", again, data)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
