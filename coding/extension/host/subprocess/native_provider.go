package subprocess

// Ports packages/coding-agent/src/core/model-runtime.ts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// nativeProviderProxy binds callbacks to one registration connection. Captured
// proxies never switch to a replacement process after reload or a crash.
type nativeProviderCallback func(json.RawMessage) (json.RawMessage, error)

type nativeProviderProxy struct {
	host         *Host
	owner        *managedExt
	registered   bool
	references   map[*Conn]map[string]struct{}
	calls        int
	conn         *Conn
	declaration  NativeProviderDeclaration
	mu           sync.Mutex
	publications map[string]nativeProviderCallback
}

func (p *nativeProviderProxy) call(ctx context.Context, args map[string]any, update func(json.RawMessage), publish nativeProviderCallback) (json.RawMessage, error) {
	p.host.mu.Lock()
	p.calls++
	p.host.mu.Unlock()
	defer func() {
		p.host.mu.Lock()
		p.calls--
		released := p.host.collectProviderObjectLocked(p)
		p.host.mu.Unlock()
		p.host.releaseProviderCallbacks(released)
	}()
	if args == nil {
		args = map[string]any{}
	}
	data, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	tool := p.declaration.Key
	env := &Envelope{Type: MsgRequest, ID: fmt.Sprintf("r%d", p.conn.nextID.Add(1)), Request: &RequestPayload{Method: MethodProviderCall, Tool: tool, Args: data}}
	if args["method"] == "getModels" || args["method"] == "getAllModels" || args["method"] == "filterModels" || args["method"] == "filterAllModels" || args["method"] == "update" {
		env.Request.Method = MethodProviderSync
	}
	if args["method"] == "stream" || args["method"] == "streamSimple" || args["method"] == "fetchDeferred" {
		env.Request.Method = MethodProviderStream
	}
	if publish != nil {
		p.mu.Lock()
		p.publications[env.ID] = publish
		p.mu.Unlock()
		defer func() { p.mu.Lock(); delete(p.publications, env.ID); p.mu.Unlock() }()
	}
	var response *Envelope
	if update == nil {
		response, err = p.conn.Request(ctx, env)
	} else {
		response, err = p.conn.requestWithUpdates(ctx, env, update)
	}
	if err != nil {
		return nil, err
	}
	if response.Response == nil {
		return nil, errors.New("native provider returned no response")
	}
	if response.Response.Error != nil {
		return nil, response.Response.Error.ToError()
	}
	return response.Response.Result, nil
}

func (p *nativeProviderProxy) carrier() *extension.NativeProvider {
	baseURL := ""
	if p.declaration.BaseURL != nil {
		baseURL = *p.declaration.BaseURL
	}
	var headers ai.ProviderHeaders
	if p.declaration.Headers != nil {
		headers = ai.ProviderHeadersFromStrings(*p.declaration.Headers)
	}
	carrier := &extension.NativeProvider{
		Headers: headers,
		IsCurrent: func() bool {
			p.host.mu.Lock()
			defer p.host.mu.Unlock()
			return p.host.nativeProviders[p.conn][p.declaration.ID] == p
		},
		ID: p.declaration.ID, Name: p.declaration.Name, BaseURL: baseURL, Models: p.declaration.Models,
		Auth:                     p.auth(context.Background()),
		FilterModels:             p.filterModels,
		CheckAuth:                p.checkAuth,
		ResolveAuth:              p.resolveAuth,
		ResolveRefreshCredential: p.refreshCredential,
		RefreshModels:            p.refreshModels,
		Stream: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return p.stream(ctx, model, transcript, options, false)
		},
		StreamSimple: func(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
			return p.stream(ctx, model, transcript, options, true)
		},
	}
	p.operations(carrier)
	return carrier
}

func (p *nativeProviderProxy) stream(ctx context.Context, model *ai.Model, transcript ai.TranscriptContext, options ai.StreamOptions, simple bool) (*ai.AssistantMessageEventStream, error) {
	method := "stream"
	if simple {
		method = "streamSimple"
	}
	return p.streamMethod(ctx, method, model, "context", map[string]any{"messages": transcript.Messages()}, nativeStreamOptions(options), options)
}

// fetchDeferred is Pi's Provider.fetchDeferred: the provider object resolves a deferred response from its handle and streams the result.
// upstream: packages/ai/src/models.ts:209-213 (Provider.fetchDeferred), models.ts StreamDeferred
func (p *nativeProviderProxy) fetchDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredFetchOptions) (*ai.AssistantMessageEventStream, error) {
	wire := nativeStreamOptions(options.StreamOptions)
	if options.Wait != nil {
		wire["wait"] = *options.Wait
	}
	return p.streamMethod(ctx, "fetchDeferred", model, "handle", handle, wire, options.StreamOptions)
}

// cancelDeferred is Pi's Provider.cancelDeferred.
// upstream: packages/ai/src/models.ts:214 (Provider.cancelDeferred)
func (p *nativeProviderProxy) cancelDeferred(ctx context.Context, model *ai.Model, handle ai.DeferredHandle, options ai.DeferredCancelOptions) error {
	_, err := p.objectCall(ctx, "cancelDeferred", map[string]any{"model": extension.ModelInfo(model), "handle": handle, "options": nativeStreamOptions(options), "callbacks": []string{}}, nil)
	return err
}

// streamMethod runs one streaming provider method (stream, streamSimple or fetchDeferred): payloadKey names the method's second argument, the transcript or the deferred handle.
func (p *nativeProviderProxy) streamMethod(ctx context.Context, method string, model *ai.Model, payloadKey string, payload any, wireOptions map[string]any, options ai.StreamOptions) (*ai.AssistantMessageEventStream, error) {
	stream := ai.NewAssistantMessageEventStream()
	callbacks := []string{}
	if options.OnPayload != nil {
		callbacks = append(callbacks, "onPayload")
	}
	// Pi's streamSimple contract has the provider invoke options.onResponse after the response and before its body
	// (coding-agent extensions/types.ts:1917-1920), which is how sdk.ts:387-433 handleProviderResponse reaches after_provider_response.
	if options.OnResponse != nil {
		callbacks = append(callbacks, "onResponse")
	}
	// The same contract lets the provider invoke options.onProviderStreamEvent(data, model) with parsed stream events before normalization
	// (extensions/types.ts:1917-1919, ai types.ts:204), which sdk.ts handleProviderStreamEvent turns into provider_stream_event.
	if options.OnProviderStreamEvent != nil {
		callbacks = append(callbacks, "onProviderStreamEvent")
	}
	args := map[string]any{"method": method, "params": map[string]any{"model": extension.ModelInfo(model), payloadKey: payload, "options": wireOptions, "callbacks": callbacks}}
	go func() {
		operation, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)
		var eventError error
		terminal := false
		_, err := p.call(operation, args, func(data json.RawMessage) {
			if string(data) == `{"type":"provider_started"}` {
				return
			}
			if eventError != nil || terminal {
				return
			}
			event, decodeErr := decodeNativeProviderEvent(data)
			if decodeErr != nil {
				eventError = decodeErr
				cancel(decodeErr)
				return
			}
			if pushErr := stream.Push(event); pushErr != nil {
				eventError = pushErr
				cancel(pushErr)
				return
			}
			terminal = event.EventType() == ai.EventDone || event.EventType() == ai.EventError
		}, func(raw json.RawMessage) (json.RawMessage, error) {
			var callback struct {
				Method string `json:"method"`
				Params struct {
					Payload json.RawMessage `json:"value"`
				} `json:"params"`
			}
			if err := json.Unmarshal(raw, &callback); err != nil {
				return nil, err
			}
			switch callback.Method {
			case "onPayload":
				if options.OnPayload == nil {
					return nil, errors.New("native stream has no payload callback")
				}
				var payload any
				if len(callback.Params.Payload) > 0 {
					if err := json.Unmarshal(callback.Params.Payload, &payload); err != nil {
						return nil, err
					}
				}
				result, err := options.OnPayload(payload, model)
				if err != nil {
					return nil, err
				}
				return json.Marshal(result)
			case "onResponse":
				if options.OnResponse == nil {
					return nil, errors.New("native stream has no response callback")
				}
				var response ai.ProviderResponse
				if err := json.Unmarshal(callback.Params.Payload, &response); err != nil {
					return nil, err
				}
				if err := options.OnResponse(operation, response, model); err != nil {
					return nil, err
				}
				return json.RawMessage("null"), nil
			case "onProviderStreamEvent":
				if options.OnProviderStreamEvent == nil {
					return nil, errors.New("native stream has no provider stream event callback")
				}
				var data any
				if len(callback.Params.Payload) > 0 {
					if err := json.Unmarshal(callback.Params.Payload, &data); err != nil {
						return nil, err
					}
				}
				if err := options.OnProviderStreamEvent(operation, data, model); err != nil {
					return nil, err
				}
				return json.RawMessage("null"), nil
			default:
				return nil, fmt.Errorf("native provider callback %q is not handled", callback.Method)
			}
		})
		if terminal {
			return
		}
		if eventError != nil {
			err = eventError
		}
		if err == nil {
			err = errors.New("native provider stream ended without a terminal event")
		}
		reason := ai.StopReasonError
		if ctx.Err() != nil {
			reason = ai.StopReasonAborted
		}
		_ = stream.Push(ai.ErrorEvent{Reason: reason, Error: &ai.AssistantMessage{Content: []ai.AssistantContentBlock{}, Provider: model.ProviderMeta.ProviderID, Model: model.ID, API: model.ProviderMeta.API, StopReason: reason, ErrorMessage: err.Error()}})
	}()
	return stream, nil
}

func nativeStreamOptions(o ai.StreamOptions) map[string]any {
	out := providerOptionsWire(o)
	if o.APIKey == "" {
		delete(out, "apiKey")
	}
	if o.OnPayload != nil {
		out["hasOnPayload"] = true
	}
	return out
}

func decodeNativeEvent[T ai.AssistantMessageEvent](data json.RawMessage) (ai.AssistantMessageEvent, error) {
	var event T
	err := json.Unmarshal(data, &event)
	return event, err
}
func decodeNativeProviderEvent(data json.RawMessage) (ai.AssistantMessageEvent, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Type {
	case "start":
		return decodeNativeEvent[ai.StartEvent](data)
	case "text_start":
		return decodeNativeEvent[ai.TextStartEvent](data)
	case "text_delta":
		return decodeNativeEvent[ai.TextDeltaEvent](data)
	case "text_end":
		return decodeNativeEvent[ai.TextEndEvent](data)
	case "thinking_start":
		return decodeNativeEvent[ai.ThinkingStartEvent](data)
	case "thinking_delta":
		return decodeNativeEvent[ai.ThinkingDeltaEvent](data)
	case "thinking_end":
		return decodeNativeEvent[ai.ThinkingEndEvent](data)
	case "toolcall_start":
		return decodeNativeEvent[ai.ToolCallStartEvent](data)
	case "toolcall_delta":
		return decodeNativeEvent[ai.ToolCallDeltaEvent](data)
	case "toolcall_end":
		return decodeNativeEvent[ai.ToolCallEndEvent](data)
	case "done":
		return decodeNativeEvent[ai.DoneEvent](data)
	case "error":
		return decodeNativeEvent[ai.ErrorEvent](data)
	default:
		return nil, fmt.Errorf("unknown native provider event %q", probe.Type)
	}
}

// registerNativeProvider binds the provider to conn, the connection of me that declared it.
func (h *Host) registerNativeProvider(ctx context.Context, me *managedExt, conn *Conn, declaration *NativeProviderDeclaration) error {
	if declaration.ID == "" || declaration.Key == "" || declaration.Auth == nil {
		return errors.New("native provider requires id, callback key and auth declaration")
	}
	declaration.Handle = rand.Text()
	p := &nativeProviderProxy{host: h, owner: me, registered: true, references: map[*Conn]map[string]struct{}{}, conn: conn, declaration: *declaration, publications: map[string]nativeProviderCallback{}}
	h.mu.Lock()
	released := h.retireNativeProviderLocked(declaration.ID)
	if h.nativeProviderHandles == nil {
		h.nativeProviderHandles = map[string]*nativeProviderProxy{}
	}
	h.nativeProviderHandles[declaration.Handle] = p
	if h.nativeProviders == nil {
		h.nativeProviders = map[*Conn]map[string]*nativeProviderProxy{}
	}
	if h.nativeProviders[conn] == nil {
		h.nativeProviders[conn] = map[string]*nativeProviderProxy{}
	}
	h.nativeProviders[conn][declaration.ID] = p
	h.mu.Unlock()
	h.releaseProviderCallbacks(released)
	var published sync.Once
	publish := func() { published.Do(func() { h.publishNativeProvider(p) }); extension.CallInitiated(ctx) }
	if err := h.providerRuntime.RegisterNativeProviderCarrier(extension.WithCallInitiation(ctx, publish), p.carrier(), extConfigOrigin(me.config)); err != nil {
		return err
	}
	publish()
	if h.uiBridge != nil {
		h.uiBridge.PublishModelCatalog()
	}
	return nil
}

func (h *Host) publishNativeProvider(p *nativeProviderProxy) {
	me, declaration := p.owner, &p.declaration
	h.mu.Lock()
	notifySuperseded := func() {}
	defer func() {
		h.mu.Unlock()
		notifySuperseded()
	}()
	if h.nativeProviders[p.conn][declaration.ID] != p {
		return
	}
	notifySuperseded = h.transferProviderOwnershipLocked(me, p.conn, declaration.ID)
	if declaration.OAuth != nil {
		ai.RegisterOAuthProvider(declaration.ID, &nativeOAuthProxy{proxy: p, host: h, owner: me.config.Name})
		if !slices.Contains(me.oauthProviderNames, declaration.ID) {
			me.oauthProviderNames = append(me.oauthProviderNames, declaration.ID)
		}
	} else if slices.Contains(me.oauthProviderNames, declaration.ID) {
		// Pi's registerNativeProvider deletes the configuration registration and its oauth (model-runtime.ts:744-751).
		ai.UnregisterOAuthProvider(declaration.ID)
		me.oauthProviderNames = slices.DeleteFunc(me.oauthProviderNames, func(name string) bool { return name == declaration.ID })
	}
	if h.uiBridge != nil {
		h.uiBridge.recordNativeProviderRegistration(declaration.ID, *declaration)
	}
}

func (h *Host) handleProviderPublication(ctx context.Context, conn *Conn, call *CallPayload) (*CallResultPayload, error) {
	var args struct {
		Provider string          `json:"provider"`
		Persist  json.RawMessage `json:"persist"`
	}
	if err := json.Unmarshal(call.Args, &args); err != nil {
		return nil, err
	}
	h.mu.Lock()
	p := h.nativeProviders[conn][args.Provider]
	if p == nil {
		for _, candidate := range h.nativeProviderHandles {
			if candidate.conn == conn && candidate.declaration.Key == args.Provider {
				p = candidate
				break
			}
		}
	}
	h.mu.Unlock()
	if p == nil {
		return nil, errors.New("native provider owner is not registered")
	}
	p.mu.Lock()
	publish := p.publications[call.ParentRequestID]
	p.mu.Unlock()
	if publish == nil {
		return nil, errors.New("native provider callback has no active request")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := publish(call.Args)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		result = json.RawMessage("null")
	}
	return &CallResultPayload{Result: result}, nil
}
