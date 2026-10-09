package extensionconformance

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi's ctx.modelRegistry.stream(model, context, options) takes the provider request callbacks onPayload, onResponse and transformHeaders
// (packages/ai/src/types.ts ProviderRequestOptions; the extension runtime forwards them through sdk.ts modelRegistry.stream).
// Every SDK passes them to the host as flags on the modelStream call and answers the host's model_stream_callback requests with the
// extension's own functions. The expected values below are written only by the extension's callbacks: the host-side StreamModel double
// calls the callbacks the production host built (extension.ModelStreamRequestFromContext) and records what they returned, so an SDK that
// never sends the flag leaves the callback nil, and one that answers with a constant fails the distinct values.
func TestModelStreamCallbacksSDKsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping model stream callback conformance in short mode (builds subprocess fixtures)")
	}
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t) // building a subprocess fixture can take minutes: the call's own deadline starts after it
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			type observed struct {
				payload    any
				payloadErr error
				respErr    error
				headers    ai.ProviderHeaders
				headersErr error
				set        [3]bool
			}
			var mu sync.Mutex
			var calls []observed
			h.bridge.SetHostAction("streamModel", func(callCtx context.Context, model map[string]any, _ map[string]any) (*ai.AssistantMessageEventStream, error) {
				provider, _ := model["provider"].(string)
				modelID, _ := model["modelId"].(string)
				request := extension.ModelStreamRequestFromContext(callCtx)
				var got observed
				got.set = [3]bool{request.OnPayload != nil, request.OnResponse != nil, request.TransformHeaders != nil}
				if request.OnPayload != nil {
					got.payload, got.payloadErr = request.OnPayload(map[string]any{"original": true}, nil)
				}
				if request.OnResponse != nil {
					got.respErr = request.OnResponse(callCtx, ai.ProviderResponse{Status: 201, Headers: map[string]string{"x-upstream": "seen"}}, nil)
				}
				if request.TransformHeaders != nil {
					one := "1"
					got.headers, got.headersErr = request.TransformHeaders(callCtx, ai.ProviderHeaders{"x-in": &one})
				}
				mu.Lock()
				calls = append(calls, got)
				mu.Unlock()
				return conformanceModelStream(provider, modelID)
			})
			command, ok := findCommand(h.runner, "model-stream-callback-probe")
			if !ok {
				t.Fatal("model-stream-callback-probe command not registered")
			}
			if err := command.Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			waitFor(t, func() bool { return slices.Contains(*h.notify, "model-callbacks=ok:info") })
			mu.Lock()
			defer mu.Unlock()
			if len(calls) != 2 {
				t.Fatalf("model calls = %d, want one with the callbacks and one without", len(calls))
			}
			with, without := calls[0], calls[1]
			if with.set != [3]bool{true, true, true} {
				t.Fatalf("callbacks the host saw = %v, want onPayload, onResponse and transformHeaders", with.set)
			}
			if with.payloadErr != nil || with.respErr != nil || with.headersErr != nil {
				t.Fatalf("callback errors = %v %v %v", with.payloadErr, with.respErr, with.headersErr)
			}
			// The extension's onPayload returns the payload with a member of its own; the member's value is not in anything the host sent.
			wantPayload := map[string]any{"original": true, "mark": "on-payload"}
			if payload, err := normalizeJSON(with.payload); err != nil || !reflect.DeepEqual(payload, wantPayload) {
				t.Fatalf("onPayload returned %#v (%v), want %#v", with.payload, err, wantPayload)
			}
			if x := with.headers["x-transformed"]; x == nil || *x != "yes" || with.headers["x-in"] == nil || *with.headers["x-in"] != "1" {
				t.Fatalf("transformHeaders returned %v, want x-in kept and x-transformed added", with.headers)
			}
			if without.set != [3]bool{} {
				t.Fatalf("a stream without callbacks reached the host with %v", without.set)
			}
		})
	}
}

func normalizeJSON(value any) (any, error) {
	raw, ok := value.(json.RawMessage)
	if !ok {
		var err error
		if raw, err = json.Marshal(value); err != nil {
			return nil, err
		}
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, errors.New("not JSON: " + err.Error())
	}
	return out, nil
}
