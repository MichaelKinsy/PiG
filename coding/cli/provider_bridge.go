package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/rpcclient"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// providerBridgeRequest is one line of the provider bridge's input. Type "stream" starts the stream named by ID; "cancel" aborts it.
type providerBridgeRequest struct {
	ID      string          `json:"id"`
	Type    string          `json:"type"`
	Simple  bool            `json:"simple,omitempty"`
	Model   map[string]any  `json:"model,omitempty"` //portlint:allow emptydrop request lines are only decoded here (the caller writes them); PiG never encodes this type
	Request json.RawMessage `json:"request,omitempty"`
	// OnPayload, OnResponse and TransformHeaders name the callbacks the caller holds. The bridge calls them with a "callback" reply and waits for the "callbackResult" request that carries CallbackID.
	OnPayload        bool            `json:"onPayload,omitempty"`
	OnResponse       bool            `json:"onResponse,omitempty"`
	TransformHeaders bool            `json:"transformHeaders,omitempty"`
	CallbackID       int             `json:"callbackId,omitempty"`
	Result           json.RawMessage `json:"result,omitempty"`
	Failure          string          `json:"failure,omitempty"`
}

// providerBridgeReply is one line of its output: an "event" per provider event in order, then exactly one "result" (the final message) or "error" (the stream could not start or the
// request is invalid) for the same ID. A "callback" reply asks the caller to run one of its callbacks (Callback, CallbackID, Value); the stream waits for the answer.
type providerBridgeReply struct {
	ID         string `json:"id"`
	Type       string `json:"type"`
	Event      any    `json:"event,omitempty"`
	Result     any    `json:"result,omitempty"`
	Error      string `json:"error,omitempty"`
	Callback   string `json:"callback,omitempty"`
	CallbackID int    `json:"callbackId,omitempty"`
	Value      any    `json:"value,omitempty"`
}

// bridgeCallbacks matches callback answers to the callbacks waiting for them.
type bridgeCallbacks struct {
	mu      sync.Mutex
	next    int
	waiting map[string]chan providerBridgeRequest
}

func (c *bridgeCallbacks) wait(id string) (int, chan providerBridgeRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	if c.waiting == nil {
		c.waiting = map[string]chan providerBridgeRequest{}
	}
	ch := make(chan providerBridgeRequest, 1)
	c.waiting[fmt.Sprintf("%s/%d", id, c.next)] = ch
	return c.next, ch
}

func (c *bridgeCallbacks) answer(request providerBridgeRequest) {
	key := fmt.Sprintf("%s/%d", request.ID, request.CallbackID)
	c.mu.Lock()
	ch := c.waiting[key]
	delete(c.waiting, key)
	c.mu.Unlock()
	if ch != nil {
		ch <- request
	}
}

// runProviderBridge serves `pig provider-bridge`: JSON Lines on stdin and stdout, a process owned by the one that started it. A process with no extension runtime (a detached
// helper or a forked worker that imports Pi's packages) reaches PiG's provider implementations through it; its lifetime is the starter's, so stdin end of file cancels every stream
// and ends the process. Streams run concurrently; cancellation reaches the provider request, and the stream still delivers its terminal aborted event, as handleModelStream does.
func runProviderBridge(in io.Reader, out io.Writer, transport ai.Transport) int {
	var writeMu sync.Mutex
	//portlint:allow jsonescape PiG-only `pig provider-bridge` JSONL with no Pi counterpart; the caller's JSON.parse reads \u003c and \u0026 as the same text
	encoder := json.NewEncoder(out)
	send := func(reply providerBridgeReply) bool {
		writeMu.Lock()
		defer writeMu.Unlock()
		return encoder.Encode(reply) == nil
	}
	var mu sync.Mutex
	cancels := map[string]context.CancelFunc{}
	var wg sync.WaitGroup
	var callbacks bridgeCallbacks
	//portlint:allow ctxbackground root of the provider-bridge process's streams: its lifetime is the starter's stdin, and end of file cancels it (stopAll below)
	root, stopAll := context.WithCancel(context.Background())
	defer stopAll()

	// The request lines are Pi's JSONL framing (rpc/jsonl.ts), read by the shared reader.
	_ = rpcclient.ReadJSONLLines(in, func(line []byte) bool {
		if len(line) == 0 {
			return true
		}
		// The request line has one strict shape: an unknown field is an error, as for every Pig-owned subprocess format.
		var request providerBridgeRequest
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		jsonErr := decoder.Decode(&request)
		switch {
		case jsonErr != nil:
			send(providerBridgeReply{ID: request.ID, Type: "error", Error: fmt.Sprintf("provider bridge: invalid request line: %v", jsonErr)})
		case request.ID == "":
			send(providerBridgeReply{Type: "error", Error: "provider bridge: invalid request line: no id"})
		case request.Type == "cancel":
			mu.Lock()
			cancel := cancels[request.ID]
			mu.Unlock()
			if cancel != nil {
				cancel()
			}
		case request.Type == "callbackResult":
			callbacks.answer(request)
		case request.Type == "stream":
			providerCtx, cancel := context.WithCancel(root)
			mu.Lock()
			if _, busy := cancels[request.ID]; busy {
				mu.Unlock()
				cancel()
				send(providerBridgeReply{ID: request.ID, Type: "error", Error: "provider bridge: stream id already in use"})
				break
			}
			cancels[request.ID] = cancel
			mu.Unlock()
			wg.Go(func() {
				defer func() {
					cancel()
					mu.Lock()
					delete(cancels, request.ID)
					mu.Unlock()
				}()
				serveBridgeStream(providerCtx, request, transport, send, &callbacks)
			})
		default:
			send(providerBridgeReply{ID: request.ID, Type: "error", Error: fmt.Sprintf("provider bridge: unknown request type %q", request.Type)})
		}
		return true
	})
	stopAll()
	wg.Wait()
	return 0
}

func serveBridgeStream(ctx context.Context, request providerBridgeRequest, transport ai.Transport, send func(providerBridgeReply) bool, callbacks *bridgeCallbacks) {
	decoded, err := decodeBridgeRequest(request.Request)
	if err != nil {
		send(providerBridgeReply{ID: request.ID, Type: "error", Error: err.Error()})
		return
	}
	stream, err := icodingagent.StreamAPILeaf(ctx, request.Simple, request.Model, decoded, transport, bridgeCallbackSet(ctx, request, send, callbacks))
	if err != nil {
		send(providerBridgeReply{ID: request.ID, Type: "error", Error: err.Error()})
		return
	}
	// Events are delivered under the process context, not the stream's: a cancelled stream still ends with the provider's terminal event.
	//portlint:allow ctxmapping events are read under the process, not the stream's ctx, so a cancelled stream still delivers its terminal aborted event, as Pi's for-await over the stream does
	for event := range stream.Events(context.Background()) {
		if !send(providerBridgeReply{ID: request.ID, Type: "event", Event: event}) {
			return
		}
	}
	send(providerBridgeReply{ID: request.ID, Type: "result", Result: stream.Result()})
}

func decodeBridgeRequest(raw json.RawMessage) (map[string]any, error) {
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("provider bridge: request: %w", err)
	}
	return request, nil
}

// bridgeCallbackSet gives the provider the callbacks the caller holds. The provider awaits each; an error rejects that request, as the extension host path does
// (coding/extension/host/subprocess/model_stream_callbacks.go).
func bridgeCallbackSet(streamCtx context.Context, request providerBridgeRequest, send func(providerBridgeReply) bool, callbacks *bridgeCallbacks) extension.ModelStreamRequest {
	invoke := func(ctx context.Context, callback string, value any) (json.RawMessage, error) {
		callbackID, answered := callbacks.wait(request.ID)
		if !send(providerBridgeReply{ID: request.ID, Type: "callback", Callback: callback, CallbackID: callbackID, Value: value}) {
			return nil, fmt.Errorf("provider bridge: caller is gone")
		}
		select {
		case answer := <-answered:
			if answer.Failure != "" {
				return nil, errors.New(answer.Failure)
			}
			return answer.Result, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	var set extension.ModelStreamRequest
	if request.OnPayload {
		set.OnPayload = func(payload any, _ *ai.Model) (any, error) {
			result, err := invoke(streamCtx, "onPayload", payload)
			if err != nil {
				return nil, err
			}
			var returned struct {
				Defined bool            `json:"defined"`
				Value   json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(result, &returned); err != nil {
				return nil, fmt.Errorf("decode onPayload callback: %w", err)
			}
			if !returned.Defined {
				return nil, nil
			}
			return returned.Value, nil
		}
	}
	if request.OnResponse {
		set.OnResponse = func(ctx context.Context, response ai.ProviderResponse, _ *ai.Model) error {
			_, err := invoke(ctx, "onResponse", response)
			return err
		}
	}
	if request.TransformHeaders {
		set.TransformHeaders = func(ctx context.Context, headers ai.ProviderHeaders) (ai.ProviderHeaders, error) {
			result, err := invoke(ctx, "transformHeaders", headers)
			if err != nil {
				return nil, err
			}
			var transformed ai.ProviderHeaders
			if err := json.Unmarshal(result, &transformed); err != nil {
				return nil, fmt.Errorf("decode transformHeaders callback: %w", err)
			}
			return transformed, nil
		}
	}
	return set
}
