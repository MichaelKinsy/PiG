package subprocess

// Ports packages/ai/src/types.ts (ProviderRequestOptions.fetch).
// Ports packages/ai/src/api/openai-completions.ts (createClient).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/MichaelKinsy/PiG/ai"
)

type modelFetchRequest struct {
	ID      string      `json:"id"`
	URL     string      `json:"url"`
	Method  string      `json:"method"`
	Headers http.Header `json:"headers"`
	Body    []byte      `json:"body,omitempty"`
}

type modelFetchResponse struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    [][2]string `json:"headers"`
	Body       bool        `json:"body"`
}

type modelFetchRead struct {
	ID   string `json:"id"`
	Size int    `json:"size"`
}

type modelFetchChunk struct {
	Data []byte `json:"data"`
	Done bool   `json:"done"`
}

type modelFetchInvoke func(context.Context, string, any) (json.RawMessage, error)

func newModelFetch(invoke modelFetchInvoke) ai.FetchFunction {
	var next atomic.Uint64
	return func(request *http.Request) (*http.Response, error) {
		id := strconv.FormatUint(next.Add(1), 10)
		value := modelFetchRequest{ID: id, URL: request.URL.String(), Method: request.Method, Headers: request.Header}
		// Providers serialize their payload before HTTP dispatch. Only the response body remains streaming across this boundary.
		if request.Body != nil {
			var err error
			value.Body, err = io.ReadAll(request.Body)
			closeErr := request.Body.Close()
			if err != nil {
				return nil, err
			}
			if closeErr != nil {
				return nil, closeErr
			}
		}
		encoded, err := invoke(request.Context(), "fetch", value)
		if err != nil {
			return nil, err
		}
		var result modelFetchResponse
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, fmt.Errorf("decode fetch response: %w", err)
		}
		header := make(http.Header)
		for _, pair := range result.Headers {
			header.Add(pair[0], pair[1])
		}
		var body io.ReadCloser = http.NoBody
		if result.Body {
			body = &modelFetchBody{ctx: request.Context(), invoke: invoke, id: id}
		}
		return &http.Response{StatusCode: result.Status, Status: strconv.Itoa(result.Status) + " " + result.StatusText, Header: header, Body: body, ContentLength: -1, Request: request}, nil
	}
}

// modelFetchBody pulls only what the consumer requests. It retains no response-sized buffer, and Close releases the reader on the originating connection even after request cancellation.
type modelFetchBody struct {
	ctx       context.Context
	invoke    modelFetchInvoke
	id        string
	closeOnce sync.Once
	closeErr  error
	done      bool
}

func (body *modelFetchBody) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if body.done {
		return 0, io.EOF
	}
	// pig additive (D19): bounded pull frames carry the unchanged response byte stream over the extension socket.
	size := min(len(buffer), 64*1024)
	encoded, err := body.invoke(body.ctx, "fetchRead", modelFetchRead{ID: body.id, Size: size})
	if err != nil {
		return 0, err
	}
	var chunk modelFetchChunk
	if err := json.Unmarshal(encoded, &chunk); err != nil {
		return 0, fmt.Errorf("decode fetch body: %w", err)
	}
	if len(chunk.Data) > size {
		return 0, fmt.Errorf("fetch body returned %d bytes for a %d-byte read", len(chunk.Data), size)
	}
	body.done = chunk.Done
	n := copy(buffer, chunk.Data)
	if body.done {
		return n, io.EOF
	}
	return n, nil
}

func (body *modelFetchBody) Close() error {
	body.closeOnce.Do(func() {
		_, body.closeErr = body.invoke(context.WithoutCancel(body.ctx), "fetchClose", map[string]string{"id": body.id})
	})
	return body.closeErr
}
