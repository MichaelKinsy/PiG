package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// Provider request callbacks of ModelRegistry.Stream, Pi's ProviderRequestOptions onPayload, onResponse and transformHeaders
// (packages/ai/src/types.ts). They are options of the stream call, keyed by Pi's names, and stay in this process: the host is told which
// exist and asks for each by name (model_stream_callback) while its provider request proceeds.
//
// OnPayloadFunc may return the replacement payload; nil keeps the payload. OnResponseFunc sees the provider's response status and headers.
// TransformHeadersFunc returns the headers the request is sent with. An error rejects the provider request.
//
// The "fetch" option is Pi's ProviderRequestOptions.fetch, the transport the provider request goes through: a [ModelFetchFunc]
// (or a plain `func(*http.Request) (*http.Response, error)`). The request reaches it with its method, URL, headers and body; the response
// body streams back to the host in bounded reads and is closed when the host closes it or the stream ends. The request's context is Pi's
// init.signal: it is cancelled when the host aborts the provider request during the fetch or a body read, when the host closes the
// response, and when the stream ends.
type (
	OnPayloadFunc        func(payload any, model map[string]any) (any, error)
	OnResponseFunc       func(response map[string]any, model map[string]any) error
	TransformHeadersFunc func(headers map[string]any, model map[string]any) (map[string]any, error)
	// ModelFetchFunc is a model request transport.
	ModelFetchFunc func(request *http.Request) (*http.Response, error)
)

type modelStreamCallbacks struct {
	model            map[string]any
	onPayload        OnPayloadFunc
	onResponse       OnResponseFunc
	transformHeaders TransformHeadersFunc
	fetch            ModelFetchFunc

	fetchMu     sync.Mutex
	fetchClosed bool
	fetches     map[string]*modelFetchEntry
}

func modelStreamCallbackOption(key string) bool {
	return key == "onPayload" || key == "onResponse" || key == "transformHeaders" || key == "fetch"
}

// takeModelStreamCallbacks reads the callbacks out of the stream options, nil when there are none. A named callback type and its plain
// function literal are both accepted.
func takeModelStreamCallbacks(options map[string]any, model map[string]any) *modelStreamCallbacks {
	callbacks := &modelStreamCallbacks{model: model}
	found := false
	switch fn := options["onPayload"].(type) {
	case OnPayloadFunc:
		callbacks.onPayload, found = fn, fn != nil
	case func(any, map[string]any) (any, error):
		callbacks.onPayload, found = fn, fn != nil
	}
	switch fn := options["onResponse"].(type) {
	case OnResponseFunc:
		callbacks.onResponse = fn
	case func(map[string]any, map[string]any) error:
		callbacks.onResponse = fn
	}
	switch fn := options["transformHeaders"].(type) {
	case TransformHeadersFunc:
		callbacks.transformHeaders = fn
	case func(map[string]any, map[string]any) (map[string]any, error):
		callbacks.transformHeaders = fn
	}
	switch fn := options["fetch"].(type) {
	case ModelFetchFunc:
		callbacks.fetch = fn
	case func(*http.Request) (*http.Response, error):
		callbacks.fetch = fn
	}
	if callbacks.onResponse != nil || callbacks.transformHeaders != nil || callbacks.fetch != nil {
		found = true
	}
	if !found {
		return nil
	}
	return callbacks
}

// flags adds the callbacks that exist to the modelStream call.
func (c *modelStreamCallbacks) flags(call map[string]any) {
	if c.onPayload != nil {
		call["onPayload"] = true
	}
	if c.onResponse != nil {
		call["onResponse"] = true
	}
	if c.transformHeaders != nil {
		call["transformHeaders"] = true
	}
	if c.fetch != nil {
		call["fetch"] = true
	}
}

// modelFetchEntry is one fetch of a stream: the cancellation of its request (Pi's init.signal) and, once the response arrived, its body.
type modelFetchEntry struct {
	cancel context.CancelFunc
	body   io.ReadCloser
}

var errModelFetchClosed = errors.New("model fetch transport is closed")

// closeFetches ends the stream's transport, as runtime-node/model-fetch.mjs disposeFetch does: it cancels every fetch still in flight and
// closes the response bodies the host did not close. A fetch that answers afterwards has its body closed and fails.
func (c *modelStreamCallbacks) closeFetches() {
	c.fetchMu.Lock()
	entries := c.fetches
	c.fetches = nil
	c.fetchClosed = true
	c.fetchMu.Unlock()
	for _, entry := range entries {
		entry.cancel()
		if entry.body != nil {
			_ = entry.body.Close()
		}
	}
}

// serveFetch answers the host's fetch, fetchRead and fetchClose. ctx is the host's request for this one callback: the host cancels it
// when the provider request is aborted, which cancels the fetch's request context while the fetch or a read is in progress.
func (c *modelStreamCallbacks) serveFetch(ctx context.Context, callback string, value json.RawMessage) (any, error) {
	switch callback {
	case "fetch":
		var call struct {
			ID      string              `json:"id"`
			URL     string              `json:"url"`
			Method  string              `json:"method"`
			Headers map[string][]string `json:"headers"`
			Body    []byte              `json:"body"`
		}
		if err := json.Unmarshal(value, &call); err != nil {
			return nil, err
		}
		var body io.Reader
		if call.Body != nil {
			body = bytes.NewReader(call.Body)
		}
		fetchCtx, cancel := context.WithCancel(context.Background())
		request, err := http.NewRequestWithContext(fetchCtx, call.Method, call.URL, body)
		if err != nil {
			cancel()
			return nil, err
		}
		request.Header = http.Header(call.Headers)
		c.fetchMu.Lock()
		if c.fetchClosed {
			c.fetchMu.Unlock()
			cancel()
			return nil, errModelFetchClosed
		}
		if c.fetches == nil {
			c.fetches = map[string]*modelFetchEntry{}
		}
		entry := &modelFetchEntry{cancel: cancel}
		c.fetches[call.ID] = entry
		c.fetchMu.Unlock()
		stop := context.AfterFunc(ctx, cancel)
		response, err := c.fetch(request)
		stop()
		hasBody := err == nil && response.Body != nil && response.Body != http.NoBody
		c.fetchMu.Lock()
		closed := c.fetchClosed || fetchCtx.Err() != nil
		if closed || !hasBody {
			if c.fetches[call.ID] == entry {
				delete(c.fetches, call.ID)
			}
		} else {
			entry.body = response.Body
		}
		c.fetchMu.Unlock()
		if err != nil {
			cancel()
			return nil, err
		}
		if closed {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			cause := context.Cause(fetchCtx)
			cancel()
			if cause == nil {
				cause = errModelFetchClosed
			}
			return nil, cause
		}
		if !hasBody {
			cancel()
		}
		headers := make([][2]string, 0, len(response.Header))
		for name, values := range response.Header {
			for _, entry := range values {
				headers = append(headers, [2]string{name, entry})
			}
		}
		return map[string]any{"status": response.StatusCode, "statusText": modelFetchStatusText(response), "headers": headers, "body": hasBody}, nil
	case "fetchRead":
		var call struct {
			ID   string `json:"id"`
			Size int    `json:"size"`
		}
		if err := json.Unmarshal(value, &call); err != nil {
			return nil, err
		}
		c.fetchMu.Lock()
		entry := c.fetches[call.ID]
		c.fetchMu.Unlock()
		if entry == nil || entry.body == nil {
			return nil, fmt.Errorf("unknown model fetch response %s", call.ID)
		}
		if call.Size < 1 || call.Size > 64*1024 {
			return nil, errors.New("invalid model fetch read size")
		}
		stop := context.AfterFunc(ctx, entry.cancel)
		defer stop()
		buffer := make([]byte, call.Size)
		n, err := entry.body.Read(buffer)
		for n == 0 && err == nil {
			n, err = entry.body.Read(buffer)
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		return map[string]any{"data": buffer[:n], "done": err == io.EOF}, nil
	default: // fetchClose
		var call struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(value, &call); err != nil {
			return nil, err
		}
		c.fetchMu.Lock()
		entry := c.fetches[call.ID]
		if entry != nil && entry.body != nil {
			delete(c.fetches, call.ID)
		}
		c.fetchMu.Unlock()
		if entry == nil || entry.body == nil {
			return nil, nil
		}
		entry.cancel()
		return nil, entry.body.Close()
	}
}

// modelFetchStatusText is the reason phrase of the response's Status, as a fetch Response's statusText: empty when the function set none.
func modelFetchStatusText(response *http.Response) string {
	code := strconv.Itoa(response.StatusCode)
	if text, ok := strings.CutPrefix(response.Status, code+" "); ok {
		return text
	}
	if response.Status == code {
		return ""
	}
	return response.Status
}

// modelStreamCallback answers the host's request for one callback of a stream in flight.
func (e *Extension) modelStreamCallback(ctx context.Context, args json.RawMessage) (any, error) {
	var request struct {
		StreamID string          `json:"streamId"`
		Callback string          `json:"callback"`
		Value    json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(args, &request); err != nil {
		return nil, fmt.Errorf("parse model_stream_callback: %w", err)
	}
	e.modelStreamsMu.RLock()
	callbacks := e.modelStreamCallbacks[request.StreamID]
	e.modelStreamsMu.RUnlock()
	if callbacks == nil && request.Callback == "fetchClose" {
		return nil, nil // a deferred Body.Close may follow the terminal stream event
	}
	if callbacks == nil {
		return nil, fmt.Errorf("unknown model stream callback %s/%s", request.StreamID, request.Callback)
	}
	switch request.Callback {
	case "fetch", "fetchRead", "fetchClose":
		if callbacks.fetch != nil {
			return callbacks.serveFetch(ctx, request.Callback, request.Value)
		}
	case "onPayload":
		if callbacks.onPayload == nil {
			break
		}
		var payload any
		if err := json.Unmarshal(request.Value, &payload); err != nil {
			return nil, err
		}
		replacement, err := callbacks.onPayload(payload, callbacks.model)
		if err != nil {
			return nil, err
		}
		return map[string]any{"defined": replacement != nil, "value": replacement}, nil
	case "onResponse":
		if callbacks.onResponse == nil {
			break
		}
		var response map[string]any
		if err := json.Unmarshal(request.Value, &response); err != nil {
			return nil, err
		}
		return nil, callbacks.onResponse(response, callbacks.model)
	case "transformHeaders":
		if callbacks.transformHeaders == nil {
			break
		}
		var headers map[string]any
		if err := json.Unmarshal(request.Value, &headers); err != nil {
			return nil, err
		}
		return callbacks.transformHeaders(headers, callbacks.model)
	}
	return nil, fmt.Errorf("unknown model stream callback %s/%s", request.StreamID, request.Callback)
}
