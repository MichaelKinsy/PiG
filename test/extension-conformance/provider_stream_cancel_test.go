package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	"github.com/MichaelKinsy/PiG/test/extension-conformance/testfixture"
)

// errHostEndsTheRequest is the cause the rows end their request's context with. The host waits for the author of a provider_stream_simple request whose context ended by a
// cancel or a deadline, the abort of a turn, and sends it a signal-only cancel (Conn.request); any other cause ends the request with an ordinary cancel, which these rows state.
var errHostEndsTheRequest = errors.New("the host ends the request")

// providerCancelCase is one SDK and load mode of a provider stream cancel row.
type providerCancelCase struct {
	name, source, provider string
	fused, packed          bool
	// answersResult marks a case whose SDK answers the cancelled request with the author's result (TestProviderStreamEndedWithAResultAfterTheCancel).
	answersResult bool
}

// providerCancelAnswer runs one case: the provider pushes start and three text_delta events, the host reads them and ends the request (errHostEndsTheRequest), and the provider, which waited for
// the cancel, does what its fixture does next. The host has the four events as pushed, the SDK forwards no later one, and the host's stream ends aborted; those are checked
// here. The host stops listening to a provider_stream_simple request at its cancel, so the SDK's answer, which is returned, is read from the frames the extension sends.
func providerCancelAnswer(t *testing.T, tc providerCancelCase) *subprocess.ResponsePayload {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	// The frames the extension sends: the stream events of the request, and its answer.
	var mu sync.Mutex
	requestID, forwarded := "", 0
	var answer *subprocess.ResponsePayload
	forwardedAtAnswer, answered := 0, make(chan struct{})
	host.SetInboundObserver(func(_ string, env *subprocess.Envelope) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case env.Notify != nil && env.Notify.Method == subprocess.NotifyProviderStreamEvent:
			var args struct {
				RequestID string `json:"request_id"`
			}
			if json.Unmarshal(env.Notify.Args, &args) == nil && (requestID == "" || requestID == args.RequestID) {
				requestID = args.RequestID
				forwarded++
			}
		case env.Response != nil && requestID != "" && env.ID == requestID && answer == nil:
			answer, forwardedAtAnswer = env.Response, forwarded
			close(answered)
		}
	})
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	if err := loadProviderFixture(t, host, tc.name, tc.source, tc.fused, tc.packed); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	model := services.ModelRuntime().GetModel(tc.provider, "faux")
	if model == nil {
		t.Fatalf("%s/faux is not registered", tc.provider)
	}
	requestContext, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	stream := services.ModelRuntime().Stream(requestContext, model, ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("go")}}}, ai.StreamOptions{APIKey: "extension-key"})
	var shown, ends []string
	for event := range stream.Events(t.Context()) {
		switch event.EventType() {
		case ai.EventStart, ai.EventTextDelta:
			text, err := partialText(event)
			if err != nil {
				t.Fatal(err)
			}
			shown = append(shown, text)
			if len(shown) == 1+testfixture.ProviderCancelBurstBefore {
				cancel(errHostEndsTheRequest)
			}
		case ai.EventDone, ai.EventError:
			raw, err := json.Marshal(event)
			if err != nil {
				t.Fatal(err)
			}
			var end struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(raw, &end); err != nil {
				t.Fatal(err)
			}
			ends = append(ends, end.Type+":"+end.Reason)
		}
	}
	if want := []string{"", "x", "xx", "xxx"}; fmt.Sprint(shown) != fmt.Sprint(want) {
		t.Errorf("the host's stream shows %q, want %q: the events pushed before the cancel, as pushed, and none after it", shown, want)
	}
	if want := []string{"error:aborted"}; fmt.Sprint(ends) != fmt.Sprint(want) {
		t.Errorf("the host's stream ends with %q, want %q", ends, want)
	}
	select {
	case <-answered:
	case <-time.After(30 * time.Second):
		t.Fatal("the extension did not answer the cancelled request")
	}
	mu.Lock()
	defer mu.Unlock()
	if forwardedAtAnswer != 1+testfixture.ProviderCancelBurstBefore {
		t.Errorf("the extension forwarded %d events before its answer, want %d: none of the events pushed after the cancel", forwardedAtAnswer, 1+testfixture.ProviderCancelBurstBefore)
	}
	return answer
}

// wantAbortAnswer fails the test unless answer is the abort error with no result.
func wantAbortAnswer(t *testing.T, answer *subprocess.ResponsePayload) {
	t.Helper()
	result := strings.TrimSpace(string(answer.Result))
	if answer.Error == nil || (result != "" && result != "null") {
		t.Fatalf("the extension answered the cancelled request with result %q and error %v, want no result and the abort error", result, answer.Error)
	}
	if message := answer.Error.ToError().Error(); !strings.Contains(message, "provider stream aborted") && !strings.Contains(message, "context canceled") {
		t.Errorf("the extension answered the cancelled request with the error %q, want the abort (\"provider stream aborted\", or Go's \"context canceled\")", message)
	}
}

// TestProviderStreamCancelledDuringAPushBurstAnswersTheAbortError: a provider pushes start and three text_delta events, the host reads them and cancels the request, and the
// provider, which waited for the cancel, goes on pushing three more and done. In every SDK and load mode the host has the four events as pushed, the SDK forwards none of the
// later ones, the host's stream ends aborted, and the SDK answers the request with the abort error and no result. The host stops listening to a provider_stream_simple request
// at its cancel, so the answer is read from the frames the extension sends.
//
// The Python SDK's read loop ends the stream of a cancelled request before it sets the request's signal; the held-signal cases hold the signal back 0.3 s in the fixture, so the
// request's thread runs between the two (it answered a null result there). Python is the one runtime with that order and with a seam to hold the signal: the Go SDK reads the
// request's context once its forwarder has stopped, the Rust SDK sets the request's flag before it ends the stream, the Lisp SDK ends the stream from a subscriber of the set
// signal, and Node aborts the signal on its one thread and never ends the author's stream. Those runtimes run the unforced row.
func TestProviderStreamCancelledDuringAPushBurstAnswersTheAbortError(t *testing.T) {
	const provider, held = "cancel-burst-provider", "cancel-burst-held-provider"
	cases := []providerCancelCase{
		{name: "go-isolated", source: "testdata/provider-go", provider: provider}, {name: "go-packed", source: "testdata/provider-go", provider: provider, packed: true},
		{name: "python-isolated", source: "testdata/provider-python", provider: provider}, {name: "python-packed", source: "testdata/provider-python", provider: provider, packed: true},
		{name: "python-isolated-held-signal", source: "testdata/provider-python", provider: held}, {name: "python-packed-held-signal", source: "testdata/provider-python", provider: held, packed: true},
		{name: "rust-isolated", source: "testdata/provider-rust", provider: provider}, {name: "rust-packed", source: "testdata/provider-rust", provider: provider, packed: true},
		{name: "lisp-isolated", source: "testdata/provider-lisp", provider: provider}, {name: "lisp-packed", source: "testdata/provider-lisp", provider: provider, packed: true},
		{name: "node-isolated", source: "testdata/provider-snapshot.mjs", provider: provider}, {name: "node-packed", source: "testdata/provider-snapshot.mjs", provider: provider, packed: true},
		{name: "fused-go", provider: provider, fused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantAbortAnswer(t, providerCancelAnswer(t, tc))
		})
	}
}

// TestProviderStreamEndedWithAResultAfterTheCancel: a provider pushes start and three text_delta events, the host reads them and cancels the request, and the provider, which
// waited for the cancel, ends its stream with the message as its result and pushes nothing more. In every SDK and load mode the host has the four events as pushed and its
// stream ends aborted. The answer on the wire is the abort error and no result in the Go, Python, Rust and Lisp SDKs, which read the cancel once more after their forwarding
// loop.
//
// SDK DRIFT, Node (the answersResult cases): the Node SDK answers the author's result. Its forwarding loop reads the request's signal when it takes an event, no event follows
// this cancel, and after the loop it answers `await stream.result()` unread (runtime-node/runtime.mjs, the provider_stream_simple case). Pi's in-process consumer has no check
// of its own either: it takes the result of a stream that ended (packages/agent/src/agent-loop.ts:424-478). The difference is on the wire only: the host fails its stream at
// the cancel and never reads the answer (coding/extension/host/subprocess/provider_stream.go, the error of requestWithUpdates), which the checks of providerCancelAnswer
// show for every case, Node's too. The cells state what each SDK answers, so a change in any of them is seen.
func TestProviderStreamEndedWithAResultAfterTheCancel(t *testing.T) {
	const provider = "cancel-end-provider"
	cases := []providerCancelCase{
		{name: "go-isolated", source: "testdata/provider-go", provider: provider}, {name: "go-packed", source: "testdata/provider-go", provider: provider, packed: true},
		{name: "python-isolated", source: "testdata/provider-python", provider: provider}, {name: "python-packed", source: "testdata/provider-python", provider: provider, packed: true},
		{name: "rust-isolated", source: "testdata/provider-rust", provider: provider}, {name: "rust-packed", source: "testdata/provider-rust", provider: provider, packed: true},
		{name: "lisp-isolated", source: "testdata/provider-lisp", provider: provider}, {name: "lisp-packed", source: "testdata/provider-lisp", provider: provider, packed: true},
		{name: "node-isolated", source: "testdata/provider-snapshot.mjs", provider: provider, answersResult: true},
		{name: "node-packed", source: "testdata/provider-snapshot.mjs", provider: provider, packed: true, answersResult: true},
		{name: "fused-go", provider: provider, fused: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			answer := providerCancelAnswer(t, tc)
			if !tc.answersResult {
				wantAbortAnswer(t, answer)
				return
			}
			var message struct {
				Content []struct{ Text string } `json:"content"`
			}
			if answer.Error != nil || json.Unmarshal(answer.Result, &message) != nil || len(message.Content) != 1 || message.Content[0].Text != "xxx" {
				t.Fatalf("the extension answered the cancelled request with result %q and error %v, want the author's result (the message with the text \"xxx\") and no error", answer.Result, answer.Error)
			}
		})
	}
}
