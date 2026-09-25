package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
)

// nodeTransportError gives Go transport failures the message emitted by the equivalent Node fetch boundary while retaining the Go cause for diagnostics.
type nodeTransportError struct {
	message string
	cause   error
}

func (err *nodeTransportError) Error() string { return err.message }
func (err *nodeTransportError) Unwrap() error { return err.cause }

func contextTransportError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

// nodeFetchTransport maps failures before response headers to the "fetch failed" rejection used by Node/undici. HTTP responses, including error statuses, remain responses. Body read failures are mapped separately because undici reports a dropped response stream as "terminated".
type nodeFetchTransport struct {
	base http.RoundTripper
}

func (transport *nodeFetchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		if contextErr := contextTransportError(request.Context()); contextErr != nil {
			return response, contextErr
		}
		return response, &nodeTransportError{message: "fetch failed", cause: err}
	}
	if response.Body != nil {
		response.Body = &nodeFetchBody{ReadCloser: response.Body, ctx: request.Context()}
	}
	return response, nil
}

type nodeFetchBody struct {
	io.ReadCloser
	ctx context.Context
}

func (body *nodeFetchBody) Read(buffer []byte) (int, error) {
	count, err := body.ReadCloser.Read(buffer)
	if err == nil || errors.Is(err, io.EOF) {
		return count, err
	}
	if contextErr := contextTransportError(body.ctx); contextErr != nil {
		return count, contextErr
	}
	return count, &nodeTransportError{message: "terminated", cause: err}
}

type connectionError interface {
	ConnectionError() bool
}

// mapBedrockTransportError applies the same categories at the AWS SDK boundary. Modeled HTTP/service errors remain untouched so status and Bedrock exception classification continue to control overflow and retry behavior.
func mapBedrockTransportError(ctx context.Context, err error, message string) error {
	if err == nil {
		return nil
	}
	if contextErr := contextTransportError(ctx); contextErr != nil {
		return contextErr
	}
	if !isGoTransportError(err) {
		return err
	}
	return &nodeTransportError{message: message, cause: err}
}

func isGoTransportError(err error) bool {
	var connection connectionError
	if errors.As(err, &connection) && connection.ConnectionError() {
		return true
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) {
		return true
	}

	// HTTP/2 errors in net/http are intentionally unexported and can reach an SDK stream without a net.Error in their chain. Keep this fallback limited to Go transport messages rather than broad provider text.
	text := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"connection reset by peer",
		"use of closed network connection",
		"http2: server sent goaway",
		"http2: client connection lost",
		"http2 stream closed",
		"tls handshake timeout",
	} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
}
