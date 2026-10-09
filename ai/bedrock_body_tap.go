//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
)

// bedrockBodyTap is the HTTP client one ConverseStream call uses. When the SDK receives a 200 response on an HTTP/1 connection whose reads the transport can observe, the tap keeps the real body for the provider and hands the SDK an empty one. The SDK's own event-stream reader is a free goroutine, so its progress would decide what a consumer observes; the provider reads the body itself as an executor turn (bedrock_stream_pipeline.go).
//
// An HTTP/2 stream, a caller-supplied HTTP client and every non-200 response reach the SDK unchanged: their reads cannot report buffered state, which the executor treats as always pending.
type bedrockBodyTap struct {
	inner bedrockHTTPClient

	mu       sync.Mutex
	owner    *bodyReadOwner
	observed *observedResponseBody
}

type bedrockHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// option installs the tap for one call.
func (tap *bedrockBodyTap) option(options *bedrockruntime.Options) {
	tap.inner = options.HTTPClient
	options.HTTPClient = tap
}

func (tap *bedrockBodyTap) Do(request *http.Request) (*http.Response, error) {
	wrote := make(chan struct{})
	var wroteOnce sync.Once
	if request.Body != nil && request.Body != http.NoBody {
		// smithy-go's ClientHandler closes the request body as soon as Do returns (transport/http/client.go:112-117). A response that arrives before the transport finished writing would then fail the write, and the transport closes the connection for a failed write, ending the stream. Closing after the write completes keeps the transport's own contract.
		request.Body = &bedrockRequestBody{ReadCloser: request.Body, wrote: wrote, done: request.Context().Done()}
	}
	trace := &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { wroteOnce.Do(func() { close(wrote) }) },
		GotConn: func(info httptrace.GotConnInfo) {
			tap.mu.Lock()
			tap.owner = httpBodyReadOwner(info.Conn)
			tap.mu.Unlock()
		},
		PutIdleConn: func(error) {
			tap.mu.Lock()
			body := tap.observed
			tap.mu.Unlock()
			if body != nil {
				body.retire()
			}
		},
	}
	response, err := tap.inner.Do(request.WithContext(httptrace.WithClientTrace(request.Context(), trace)))
	if err != nil {
		// A transport that failed before writing (refused dial, proxy error) never reports WroteRequest, and the request context may never be canceled. No write is pending once Do has failed, so release the deferred close.
		wroteOnce.Do(func() { close(wrote) })
	}
	if err != nil || response.StatusCode != http.StatusOK || response.ProtoMajor != 1 || response.Body == nil {
		return response, err
	}
	tap.mu.Lock()
	defer tap.mu.Unlock()
	if tap.owner == nil {
		return response, nil
	}
	if tap.observed != nil {
		_ = tap.observed.Close()
	}
	tap.observed = &observedResponseBody{ReadCloser: response.Body, owner: tap.owner}
	response.Body = io.NopCloser(bytes.NewReader(nil))
	return response, nil
}

// body is the kept response body, or nil when the SDK owns it.
func (tap *bedrockBodyTap) body() *observedResponseBody {
	tap.mu.Lock()
	defer tap.mu.Unlock()
	return tap.observed
}

// close releases a kept body that no pipeline took over.
func (tap *bedrockBodyTap) close() {
	if body := tap.body(); body != nil {
		_ = body.Close()
	}
}

// bedrockObservedDial wraps each connection the AWS SDK's transport dials so that body reads announce a network wait (idleTimeoutConn). Its deadlines stay inactive: the SDK owns request timeouts.
func bedrockObservedDial(dial func(context.Context, string, string) (net.Conn, error)) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, network, address)
		if err != nil {
			return nil, err
		}
		return &idleTimeoutConn{Conn: conn, timeout: configuredHTTPIdleTimeout, ready: new(atomic.Bool)}, nil
	}
}

// bedrockRequestBody defers Close until the transport has finished writing the request or the request is canceled.
type bedrockRequestBody struct {
	io.ReadCloser
	wrote <-chan struct{}
	done  <-chan struct{}
	once  sync.Once
}

func (body *bedrockRequestBody) Close() error {
	select {
	case <-body.wrote:
		return body.closeOnce()
	default:
	}
	go func() {
		select {
		case <-body.wrote:
		case <-body.done:
		}
		_ = body.closeOnce()
	}()
	return nil
}

func (body *bedrockRequestBody) closeOnce() error {
	var err error
	body.once.Do(func() { err = body.ReadCloser.Close() })
	return err
}
