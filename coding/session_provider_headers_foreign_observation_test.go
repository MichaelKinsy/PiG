package coding

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// TestBeforeProviderHeadersForeignGoroutineKeepsInjectedHeaderAndNeverRaces reproduces the exact
// production failure in which ModelRuntime.StreamSimple spawns runtime.start on a separate goroutine
// with outer.ObservationContext(ctx) while the agent loop's RunStreamContinuation turn is still
// active (before observation.Yield clears executor.observation).
//
// A subprocess before_provider_headers handler on that spawned goroutine runs host.go:5112-5114:
// it calls ai.StreamObservationFromContext(parent) and, if non-nil, wraps its IPC wait in
// observation.AwaitExternal(request). Under the forced ordering where the handler looks up the
// observation while the agent loop still holds its turn and then waits while the agent loop parks
// on the stream iterator:
//   - On the unfixed base (plain build): iter.Pull panics ("yield called again before next"),
//     recovered by inproc.callHandler as a before_provider_headers extension error, silently
//     dropping the header the extension injected while the request is still sent (or crashing
//     Session.Send / deadlocking serve() under concurrent interleavings); under -race it races
//     continuationTurn.deferred and deadlocks racePull.
//   - With the ownership check in StreamObservationFromContext: the spawned goroutine gets nil,
//     runs its request wait directly, injects x-probe-injected="yes" onto request 1 with zero
//     extension errors, and the prompt and subsequent prompt complete normally.
func TestBeforeProviderHeadersForeignGoroutineKeepsInjectedHeaderAndNeverRaces(t *testing.T) {
	wait := testbudget.Wait(t)
	var mu sync.Mutex
	var recordedHeaders []http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		recordedHeaders = append(recordedHeaders, r.Header.Clone())
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"test\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()

	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{ProviderID: "test", Model: "probe", APIKey: "fixture-key", BaseURL: server.URL})
	defer func() { _ = provider.Close() }()

	handlerLookedUp := make(chan struct{})
	allowAwait := make(chan struct{})
	var lookupOnce, allowOnce sync.Once
	releaseAwait := func() { allowOnce.Do(func() { close(allowAwait) }) }
	defer releaseAwait()

	var extErrors []extension.ExtensionError
	ext := extension.Extension{
		Path: "/ext/headers-foreign-observation",
		Handlers: map[string][]extension.HandlerFn{
			"before_provider_headers": {func(args ...any) (any, error) {
				ev := args[0].(extension.BeforeProviderHeadersEvent)
				parent := args[1].(context.Context)
				observation := ai.StreamObservationFromContext(parent)
				lookupOnce.Do(func() { close(handlerLookedUp) })
				select {
				case <-allowAwait:
				case <-parent.Done():
					return nil, parent.Err()
				}
				request := func() error {
					ev.Headers["x-probe-injected"] = new("yes")
					return nil
				}
				if observation != nil {
					_ = observation.AwaitExternal(request)
				} else {
					_ = request()
				}
				return nil, nil
			}},
		},
	}

	runner := inproc.NewRunner([]extension.Extension{ext}, t.TempDir())
	runner.AddErrorListener(func(err *extension.ExtensionError) {
		mu.Lock()
		extErrors = append(extErrors, *err)
		mu.Unlock()
	})

	sess, err := NewSession(newTestServices(t), SessionOptions{
		Model:            &ai.Model{ID: "probe", Provider: provider},
		Runner:           runner,
		SkipBuiltinTools: true,
		NoSession:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for range sess.Events() {
		}
	}()
	defer func() {
		_ = sess.Close()
		<-drainDone
	}()

	// Hold the agent loop right after StreamSimple returns (before agent_loop.go:558 observation.Yield)
	// until the spawned ModelRuntime.start goroutine has reached before_provider_headers and called
	// StreamObservationFromContext. Then release the agent loop into Yield -> ObservePublishedEvents
	// and wait for the stream iterator to register a waiter (deterministic, zero sleeps) before
	// letting the handler execute its AwaitExternal / request wait.
	origStreamFn := sess.Agent().EffectiveStreamFunction()
	turnCount := 0
	sess.Agent().SetStreamFunction(func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
		turnCount++
		stream, err := origStreamFn(ctx, model, transcript, options)
		if turnCount == 1 {
			select {
			case <-handlerLookedUp:
			case <-time.After(wait):
				t.Error("before_provider_headers handler never reached StreamObservationFromContext")
				releaseAwait()
				return stream, err
			}
			// Wait until the agent loop has left Yield and is subscribed to stream (or the wait expires),
			// then allow the handler to proceed. Stream.ResultContext/Events waiters are internal to ai,
			// so observe when the agent loop releases its turn by running a one-shot StreamContinuation
			// on the same executor: it can only run once the agent loop has yielded/parked!
			go func() {
				_ = ai.RunStreamContinuation(ctx, func(*ai.StreamObservation) error {
					releaseAwait()
					return nil
				})
			}()
		}
		return stream, err
	})

	sendCtx, cancelSend := context.WithTimeout(t.Context(), wait)
	defer cancelSend()
	sendDone := make(chan error, 1)
	go func() {
		_, err := sess.Send(sendCtx, "prompt-1")
		sendDone <- err
	}()

	select {
	case err := <-sendDone:
		if err != nil {
			t.Fatalf("prompt 1 failed: %v", err)
		}
	case <-time.After(wait):
		t.Fatal("prompt 1 deadlocked when before_provider_headers ran concurrently with agent loop Yield")
	}

	mu.Lock()
	gotErrors := append([]extension.ExtensionError(nil), extErrors...)
	gotHeaders := append([]http.Header(nil), recordedHeaders...)
	mu.Unlock()
	if len(gotErrors) != 0 {
		t.Fatalf("before_provider_headers reported unexpected extension errors: %+v", gotErrors)
	}
	if len(gotHeaders) != 1 {
		t.Fatalf("server recorded %d requests for prompt 1, want 1", len(gotHeaders))
	}
	if got := gotHeaders[0].Get("x-probe-injected"); got != "yes" {
		t.Fatalf("request 1 header x-probe-injected = %q, want %q (header dropped by foreign observation race)", got, "yes")
	}

	// Verify a second prompt on the same session also injects the header cleanly.
	if _, err := sess.Send(sendCtx, "prompt-2"); err != nil {
		t.Fatalf("prompt 2 failed: %v", err)
	}
	mu.Lock()
	gotHeaders = append([]http.Header(nil), recordedHeaders...)
	mu.Unlock()
	if len(gotHeaders) != 2 || gotHeaders[1].Get("x-probe-injected") != "yes" {
		t.Fatalf("request 2 header x-probe-injected = %q (total requests %d), want %q", gotHeaders[len(gotHeaders)-1].Get("x-probe-injected"), len(gotHeaders), "yes")
	}
}
