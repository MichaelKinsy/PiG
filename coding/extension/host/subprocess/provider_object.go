package subprocess

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func (h *Host) forgetNativeProvidersLocked(conn *Conn) []*nativeProviderProxy {
	delete(h.nativeProviders, conn)
	var released []*nativeProviderProxy
	for handle, provider := range h.nativeProviderHandles {
		if provider.conn == conn {
			delete(h.nativeProviderHandles, handle)
		} else {
			delete(provider.references, conn)
			released = append(released, h.collectProviderObjectLocked(provider)...)
		}
	}
	return released
}

// Ports packages/coding-agent/src/core/model-registry.ts
// Provider object calls do not resolve host credentials or normalize transcripts: those belong to the calling Models collection, not the Provider itself.
func (h *Host) handleProviderObject(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var request ProviderObjectCall
	if err := json.Unmarshal(call.Args, &request); err != nil {
		return nil, err
	}
	h.mu.Lock()
	provider := h.nativeProviderHandles[request.Handle]
	h.mu.Unlock()
	if provider == nil {
		return nil, errors.New("Provider object owner is no longer connected")
	}
	if request.Method != "update" && !slices.Contains(provider.declaration.Methods, request.Method) {
		return nil, fmt.Errorf("Provider %s has no %s method", provider.declaration.ID, request.Method)
	}
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	if h.uiBridge != nil {
		id := request.StreamID
		if id == "" {
			id = request.CallbackID
		}
		key := modelStreamKey{owner: conn, streamID: id}
		h.uiBridge.modelStreamMu.Lock()
		h.uiBridge.modelStreams[key] = cancel
		h.uiBridge.modelStreamMu.Unlock()
		defer func() {
			h.uiBridge.modelStreamMu.Lock()
			delete(h.uiBridge.modelStreams, key)
			h.uiBridge.modelStreamMu.Unlock()
		}()
	}
	var update func(json.RawMessage)
	if request.StreamID != "" {
		update = func(event json.RawMessage) {
			var kind struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(event, &kind); err != nil {
				cancel()
				return
			}
			body := map[string]any{"streamId": request.StreamID, "event": event}
			if kind.Type == "provider_started" {
				body = map[string]any{"streamId": request.StreamID, "started": true}
			}
			args, err := json.Marshal(body)
			if err == nil {
				_ = conn.Send(&Envelope{Type: MsgNotify, Notify: &NotifyPayload{Method: "model_stream_event", Args: args}})
			}
		}
	}
	var callback nativeProviderCallback
	if request.CallbackID != "" {
		callback = func(args json.RawMessage) (json.RawMessage, error) {
			var kind struct {
				Method string `json:"method"`
			}
			if err := json.Unmarshal(args, &kind); err != nil {
				return nil, err
			}
			method := MethodProviderObjectCallback
			if kind.Method == "notify" {
				method = "provider_object_callback_sync"
			}
			reply, err := conn.Request(ctx, &Envelope{Type: MsgRequest, ID: fmt.Sprintf("r%d", conn.nextID.Add(1)), Request: &RequestPayload{Method: method, Tool: request.CallbackID, Args: args}})
			if err != nil {
				return nil, err
			}
			if reply.Response == nil {
				return nil, errors.New("Provider callback returned no response")
			}
			if reply.Response.Error != nil {
				return nil, reply.Response.Error.ToError()
			}
			return reply.Response.Result, nil
		}
	}
	extension.CallInitiated(ctx)
	result, err := provider.call(operation, map[string]any{"method": request.Method, "params": request.Params}, update, callback)
	if err != nil {
		return nil, err
	}
	return &CallResultPayload{Result: result}, nil
}
