package extensionconformance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
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
// request's context once its forwarder has stopped, the Rust SDK sets the request's flag before it ends the stream, the Lisp SDK marks the request cancelled before it sets the signal and ends the
// stream, and Node aborts the signal on its one thread and never ends the author's stream. Those runtimes run the unforced row.
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

// TestProviderStreamEndedWithAResultAfterTheCancel: a provider pushes start and three text_delta events, the host reads them and ends the request, and the provider, which
// waited for the cancel, ends its stream with the message as its result and pushes nothing more. In every SDK and load mode the host has the four events as pushed, its
// stream ends aborted, and the answer on the wire is the abort error and no result: every SDK reads the ordinary cancel once more after its forwarding loop. (The abort of a
// turn is not this cancel: it is signal-only, and its answer is the author's result, TestAbortedProviderTurnKeepsWhatTheAuthorsStreamGives.)
func TestProviderStreamEndedWithAResultAfterTheCancel(t *testing.T) {
	for _, tc := range providerRealizations("cancel-end-provider") {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			wantAbortAnswer(t, providerCancelAnswer(t, tc))
		})
	}
}

// providerRealizations are the SDKs and load modes of a provider row: every SDK alone in its process and packed with a sibling, and the Go factory fused.
func providerRealizations(provider string) []providerCancelCase {
	return []providerCancelCase{
		{name: "go-isolated", source: "testdata/provider-go", provider: provider}, {name: "go-packed", source: "testdata/provider-go", provider: provider, packed: true},
		{name: "python-isolated", source: "testdata/provider-python", provider: provider}, {name: "python-packed", source: "testdata/provider-python", provider: provider, packed: true},
		{name: "rust-isolated", source: "testdata/provider-rust", provider: provider}, {name: "rust-packed", source: "testdata/provider-rust", provider: provider, packed: true},
		{name: "lisp-isolated", source: "testdata/provider-lisp", provider: provider}, {name: "lisp-packed", source: "testdata/provider-lisp", provider: provider, packed: true},
		{name: "node-isolated", source: "testdata/provider-snapshot.mjs", provider: provider}, {name: "node-packed", source: "testdata/provider-snapshot.mjs", provider: provider, packed: true},
		{name: "fused-go", provider: provider, fused: true},
	}
}

// providerAbortFinal is what the rows of an aborted turn compare of its final assistant message.
type providerAbortFinal struct {
	blocks                         int
	text, stopReason, errorMessage string
}

func providerAbortFinalOf(t *testing.T, message any) providerAbortFinal {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	final := providerAbortFinal{blocks: len(decoded.Content), stopReason: decoded.StopReason, errorMessage: decoded.ErrorMessage}
	for _, block := range decoded.Content {
		if block.Type == "text" {
			final.text += block.Text
		}
	}
	return final
}

// abortedProviderTurn runs one turn of a Session whose model is the fixture's provider and aborts it, as the user does: when the turn's updates show the three text_delta
// events the provider pushes before it waits for its signal, or, for a provider that shows nothing before the abort (atRequest), when the host writes the provider's
// request. It returns the turn's final assistant message and the text its updates showed last.
func abortedProviderTurn(t *testing.T, tc providerCancelCase, atRequest bool) (providerAbortFinal, string) {
	t.Helper()
	services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	host := subprocess.NewHost(t.TempDir())
	defer host.Shutdown("test done")
	// The abort runs on its own goroutine: it is asked for by the Session's listener or by the connection's write loop, and neither may wait for the turn.
	var session *coding.Session
	sessionReady, aborted := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	abort := func() {
		once.Do(func() {
			go func() {
				<-sessionReady
				aborted <- session.Abort(context.Background())
			}()
		})
	}
	if atRequest {
		host.SetOutboundObserver(func(_ string, frame []byte) {
			if bytes.Contains(frame, []byte(`"provider_stream_simple"`)) {
				abort()
			}
		})
	}
	host.SetProviderCallbacks(services.Registry().RegisterExtensionProvider, services.Registry().UnregisterProvider)
	if err := loadProviderFixture(t, host, tc.name, tc.source, tc.fused, tc.packed); err != nil {
		t.Fatal(err)
	}
	applyQueuedRegistrations(host)
	model := services.ModelRuntime().GetModel(tc.provider, "faux")
	if model == nil {
		t.Fatalf("%s/faux is not registered", tc.provider)
	}
	session, err = coding.NewSession(services, coding.SessionOptions{Model: model, NoSession: true, SkipBuiltinTools: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	close(sessionReady)
	var mu sync.Mutex
	shown := ""
	var finals []providerAbortFinal
	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		switch event := event.(type) {
		case agent.MessageUpdateEvent:
			if event.AssistantMessageEvent == nil || event.AssistantMessageEvent.EventType() != ai.EventTextDelta {
				return
			}
			text, err := partialText(event.AssistantMessageEvent)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			shown = text
			mu.Unlock()
			if !atRequest && text == strings.Repeat("x", testfixture.ProviderCancelBurstBefore) {
				abort()
			}
		case agent.MessageEndEvent:
			if event.Message.Assistant != nil {
				final := providerAbortFinalOf(t, event.Message.Assistant)
				mu.Lock()
				finals = append(finals, final)
				mu.Unlock()
			}
		}
	})
	defer unsubscribe()
	ended := make(chan error, 1)
	go func() { ended <- session.Prompt(t.Context(), "go") }()
	select {
	case err := <-ended:
		if err != nil {
			t.Logf("the aborted turn's prompt returned %v", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the aborted turn did not end within 60 s")
	}
	select {
	case err := <-aborted:
		if err != nil {
			t.Errorf("abort: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the turn ended and its abort did not return, or the turn was never aborted")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(finals) != 1 {
		t.Fatalf("the aborted turn ended with %d assistant messages (%+v), want one", len(finals), finals)
	}
	return finals[0], shown
}

// TestAbortedProviderTurnKeepsWhatTheAuthorsStreamGives: Pi's consumer of a response has no check of the abort signal (packages/agent/src/agent-loop.ts:424-478), so what the
// stream of an extension's streamSimple does after the abort of the turn decides the turn's final message. The host's cancel of the provider's request is signal-only: the
// author's signal aborts and its request goes on. In every SDK and load mode, a real Session's aborted turn ends with:
//
//   - author-error: the author pushes an error event that holds its partial. The message is that partial, with its stopReason and its errorMessage.
//   - author-result: the author ends its stream with a result and no terminal event. The message is the result, stopReason stop (agent-loop.ts:470-478, `await result()`).
//   - author-ignores: the author goes on pushing and completes. The message is the whole response, stopReason stop, and the updates show the text pushed after the abort.
//   - author-throws: the author's streamSimple fails once its signal aborts. The message is empty and aborted, and its errorMessage is the author's error
//     (packages/agent/src/agent.ts:535-552, handleRunFailure).
//
// Each expected value is the fixture's own: an SDK that ends the request at the abort gives none of them (it answers "provider stream aborted", or Go's "context canceled",
// and the turn's message is empty). Pi 1.5.2 gives these four in an RPC session (coding/cli TestRPCAbortedExtensionProviderTurnKeepsWhatTheAuthorGives).
func TestAbortedProviderTurnKeepsWhatTheAuthorsStreamGives(t *testing.T) {
	before := strings.Repeat("x", testfixture.ProviderCancelBurstBefore)
	rows := []struct {
		name, provider string
		want           providerAbortFinal
		shown          string
		// atRequest: the author shows nothing before the abort, so the turn is aborted when the host writes its request.
		atRequest bool
	}{
		{name: "author-error", provider: "cancel-error-provider", want: providerAbortFinal{blocks: 1, text: before, stopReason: "aborted", errorMessage: testfixture.ProviderCancelErrorMessage}, shown: before},
		{name: "author-result", provider: "cancel-end-provider", want: providerAbortFinal{blocks: 1, text: before, stopReason: "stop"}, shown: before},
		{name: "author-ignores", provider: "cancel-burst-provider", want: providerAbortFinal{blocks: 1, text: before + before, stopReason: "stop"}, shown: before + before},
		{name: "author-throws", provider: "cancel-throw-provider", want: providerAbortFinal{stopReason: "aborted", errorMessage: testfixture.ProviderCancelThrowMessage}, atRequest: true},
	}
	for _, row := range rows {
		for _, tc := range providerRealizations(row.provider) {
			t.Run(row.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				final, shown := abortedProviderTurn(t, tc, row.atRequest)
				if final != row.want {
					t.Errorf("the aborted turn's final message is %+v, want %+v: what the author's stream gave after the abort", final, row.want)
				}
				if shown != row.shown {
					t.Errorf("the turn's updates show %q, want %q", shown, row.shown)
				}
			})
		}
	}
}
