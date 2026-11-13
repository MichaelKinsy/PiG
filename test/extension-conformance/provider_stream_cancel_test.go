package extensionconformance

import (
	"context"
	"encoding/json"
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

// TestProviderStreamCancelledDuringAPushBurstAnswersTheAbortError: a provider pushes start and three text_delta events, the host reads them and cancels the request, and the
// provider, which waited for the cancel, goes on pushing three more and done. In every SDK and load mode the host has the four events as pushed, the SDK forwards none of the
// later ones, the host's stream ends aborted, and the SDK answers the request with the abort error and no result. The host stops listening to a provider_stream_simple request
// at its cancel, so the answer is read from the frames the extension sends.
//
// The Python SDK's read loop ends the stream of a cancelled request before it sets the request's signal; the held-signal cases hold the signal back 0.3 s in the fixture, so the
// request's thread runs between the two (it answered a null result there). Python is the one runtime with that order and with a seam to hold the signal: Go's forwarder waits on
// the request's context itself, the Rust SDK sets the request's flag before it ends the stream, the Lisp SDK ends the stream from a subscriber of the set signal, and Node aborts
// the signal on its one thread and never ends the author's stream. Those runtimes run the unforced row.
func TestProviderStreamCancelledDuringAPushBurstAnswersTheAbortError(t *testing.T) {
	const provider, held = "cancel-burst-provider", "cancel-burst-held-provider"
	cases := []struct {
		name, source, provider string
		fused, packed          bool
	}{
		{"go-isolated", "testdata/provider-go", provider, false, false}, {"go-packed", "testdata/provider-go", provider, false, true},
		{"python-isolated", "testdata/provider-python", provider, false, false}, {"python-packed", "testdata/provider-python", provider, false, true},
		{"python-isolated-held-signal", "testdata/provider-python", held, false, false}, {"python-packed-held-signal", "testdata/provider-python", held, false, true},
		{"rust-isolated", "testdata/provider-rust", provider, false, false}, {"rust-packed", "testdata/provider-rust", provider, false, true},
		{"lisp-isolated", "testdata/provider-lisp", provider, false, false}, {"lisp-packed", "testdata/provider-lisp", provider, false, true},
		{"node-isolated", "testdata/provider-snapshot.mjs", provider, false, false}, {"node-packed", "testdata/provider-snapshot.mjs", provider, false, true},
		{"fused-go", "", provider, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
			requestContext, cancel := context.WithCancel(t.Context())
			defer cancel()
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
						cancel()
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
			result := strings.TrimSpace(string(answer.Result))
			if answer.Error == nil || (result != "" && result != "null") {
				t.Fatalf("the extension answered the cancelled request with result %q and error %v, want no result and the abort error", result, answer.Error)
			}
			if message := answer.Error.ToError().Error(); !strings.Contains(message, "provider stream aborted") && !strings.Contains(message, "context canceled") {
				t.Errorf("the extension answered the cancelled request with the error %q, want the abort (\"provider stream aborted\", or Go's \"context canceled\")", message)
			}
		})
	}
}
