package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/internal/lazyregexp"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

// nodeTransportError gives Go transport failures the message emitted by the equivalent Node fetch boundary while retaining the Go cause for diagnostics.
type nodeTransportError struct {
	message string
	cause   error
	// withCause appends the cause to the message. pig divergence (D144): undici's "terminated" carries the real failure in error.cause and Pi drops it (Pi #10697).
	withCause bool
	// headersTimeout marks a "fetch failed" whose undici cause is HeadersTimeoutError (code UND_ERR_HEADERS_TIMEOUT): the idle deadline passed on an established connection before the response headers arrived.
	headersTimeout bool
}

func (err *nodeTransportError) Error() string {
	if err.withCause && err.cause != nil {
		return err.message + " (cause: " + err.cause.Error() + ")"
	}
	return err.message
}
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

// fetchAbortError is what undici rejects fetch() and a pending Web Streams read with when the request signal aborts: the signal's reason. A plain controller.abort() gives DOMException AbortError, whose message is "This operation was aborted". A timeout or a caller-supplied cause keeps its own error, so provider-specific timeout mapping still sees it.
func fetchAbortError(ctx context.Context) error {
	cause := contextTransportError(ctx)
	if cause == nil {
		return nil
	}
	if errors.Is(cause, context.Canceled) {
		return &nodeTransportError{message: "This operation was aborted", cause: cause}
	}
	return cause
}

// nodeFetchTransport maps failures before response headers to the "fetch failed" rejection used by Node/undici. HTTP responses, including error statuses, remain responses. Body read failures are mapped separately because undici reports a dropped response stream as "terminated".
type nodeFetchTransport struct {
	base           http.RoundTripper
	connectionIdle bool
}

func (transport *nodeFetchTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	prepared := request
	var responseMu sync.Mutex
	var owner *bodyReadOwner
	var observed *observedResponseBody
	gotConn := false
	if transport.connectionIdle {
		// Only the owned transport needs idle activation. Caller-provided fetch clients retain the original request context identity.
		trace := &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				activateHTTPIdleTimeout(info.Conn)
				responseMu.Lock()
				owner = httpBodyReadOwner(info.Conn)
				gotConn = true
				responseMu.Unlock()
			},
			PutIdleConn: func(error) {
				responseMu.Lock()
				body := observed
				responseMu.Unlock()
				if body != nil {
					body.retire()
				}
			},
		}
		prepared = request.WithContext(httptrace.WithClientTrace(request.Context(), trace))
		prepared = originFormRequest(prepared)
	}
	response, err := transport.base.RoundTrip(prepared)
	if err != nil {
		if contextErr := fetchAbortError(request.Context()); contextErr != nil {
			return response, contextErr
		}
		responseMu.Lock()
		headersTimeout := gotConn && isHeadersTimeout(err)
		responseMu.Unlock()
		return response, &nodeTransportError{message: "fetch failed", cause: err, headersTimeout: headersTimeout}
	}
	if response.Body != nil {
		response.Body = &nodeFetchBody{ReadCloser: response.Body, ctx: request.Context()}
		responseMu.Lock()
		if owner != nil && response.ProtoMajor == 1 {
			observed = &observedResponseBody{ReadCloser: response.Body, owner: owner}
			response.Body = observed
		} else {
			// A caller-supplied fetch or HTTP/2 body does not expose whether bytes are buffered. Treating every read as awaiting the network keeps the observation deterministic: the consumer never sees data the producer has not yet been resumed for.
			response.Body = &observedResponseBody{ReadCloser: response.Body, owner: &bodyReadOwner{}, alwaysPending: true}
		}
		responseMu.Unlock()
	}
	return response, nil
}

// isHeadersTimeout reports whether a failure on an established connection is the idle deadline passing before the response headers: net/http's ResponseHeaderTimeout or the connection read/write deadline. A request-context failure never reaches it: RoundTrip returns fetchAbortError first.
func isHeadersTimeout(err error) bool {
	timeout, ok := errors.AsType[net.Error](err)
	return ok && timeout.Timeout()
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
	if contextErr := fetchAbortError(body.ctx); contextErr != nil {
		return count, contextErr
	}
	return count, &nodeTransportError{message: "terminated", cause: err, withCause: true}
}

// openAISDKTransportError reports the OpenAI SDK rejection for a request that failed before response headers. Abort and caller cancellation are left to the caller, which owns those messages.
//
// upstream: openai@7.19.0 client.js:makeRequest (lines 793-794, 820-841) throws APIConnectionTimeoutError when the fetch rejection is an AbortError or /timed? ?out/i matches String(rejection) + String(rejection.cause), and APIConnectionError ("Connection error.") otherwise (core/error.js:78-92). The timeout message is "Request timed out." unless the rejection's cause has code UND_ERR_HEADERS_TIMEOUT, which gets the SDK's headers-timeout message. Returns nil when err is not such a failure.
func openAISDKTransportError(ctx context.Context, err error) error {
	if err == nil || contextTransportError(ctx) != nil {
		return nil
	}
	rejection, cause, transport := openAISDKFetchRejection(err)
	if !transport {
		return nil
	}
	message := "Connection error."
	if isOpenAISDKTimeout(rejection, cause) {
		message = "Request timed out."
		if node, ok := rejection.(*nodeTransportError); ok && node.headersTimeout { //nolint:errorlint // The SDK reads the rejection's own cause code, not a wrapped one.
			message = openAISDKHeadersTimeoutMessage
		}
	}
	return &nodeTransportError{message: message, cause: err}
}

// openAISDKFetchRejection returns the error the SDK sees fetch reject with, and that error's immediate cause.
//
// The owned transport rejects with the Node "fetch failed" TypeError whose cause is the Go transport error. A caller-supplied fetch client is run through http.Client, which adds a *url.Error carrying the request URL; the caller's own error is the rejection and the URL decoration is not part of the SDK's text. The per-attempt timer (providerRequestTransport) aborts the request with a bare context.DeadlineExceeded, which is the SDK's AbortError; it is the cause of the "fetch failed" rejection, or the bare *url.Error error where no Node fetch layer wraps the client.
func openAISDKFetchRejection(err error) (rejection, cause error, transport bool) {
	node, ok := errors.AsType[*nodeTransportError](err)
	if !ok {
		if requestError, isURL := err.(*url.Error); isURL && requestError.Err == context.DeadlineExceeded { //nolint:errorlint // Only the bare timer abort is the SDK's AbortError.
			return requestError.Err, nil, true
		}
		return nil, nil, false
	}
	if node.cause == context.DeadlineExceeded { //nolint:errorlint // Only the bare timer abort is the SDK's AbortError.
		return node.cause, nil, true
	}
	if fetched, ok := node.cause.(*url.Error); ok { //nolint:errorlint // Only the direct client.Do decoration is stripped.
		return fetched.Err, errors.Unwrap(fetched.Err), true
	}
	return node, node.cause, true
}

// upstream: openai@7.19.0 client.js:makeRequest
const openAISDKHeadersTimeoutMessage = "Request timed out. Node.js fetch timed out waiting for response headers; configure a matching undici fetch and fetchOptions.dispatcher with an Agent whose headersTimeout is at least the SDK timeout."

var openAISDKTimeoutPattern = lazyregexp.New(`(?i)timed? ?out`)

// isOpenAISDKTimeout applies openai@7.19.0 client.js:793-794 to the fetch rejection: the SDK's own timer abort (context.DeadlineExceeded, the AbortError analog), or timeout wording in the rejection text or its immediate cause text. It does not search deeper causes and does not consult net.Error.Timeout().
func isOpenAISDKTimeout(rejection, cause error) bool {
	if rejection == context.DeadlineExceeded { //nolint:errorlint // The SDK abort is the bare deadline error, not a wrapped one.
		return true
	}
	text := rejection.Error()
	if cause != nil {
		text += cause.Error()
	}
	return openAISDKTimeoutPattern.MatchString(text)
}

type connectionError interface {
	ConnectionError() bool
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

// nodeNetErrorMessage returns the message Node's net and dns modules give a failed TCP connect or host lookup, which an SDK
// built on Node's http module (the AWS SDK's NodeHttpHandler, google-auth-library's gaxios) reports unchanged: "connect
// ECONNREFUSED 127.0.0.1:9" (net.js exceptionWithHostPort: syscall, code, address:port) and "getaddrinfo ENOTFOUND host"
// (dns.js). ok is false for any other failure, whose Node text Go's error cannot determine.
func nodeNetErrorMessage(err error) (message string, ok bool) {
	if dnsError, isDNS := errors.AsType[*net.DNSError](err); isDNS {
		switch {
		case dnsError.IsNotFound:
			return "getaddrinfo ENOTFOUND " + dnsError.Name, true
		case dnsError.IsTemporary || dnsError.IsTimeout:
			return "getaddrinfo EAI_AGAIN " + dnsError.Name, true
		}
		return "", false
	}
	// A proxy failure is a "proxyconnect" OpError around the dial to the proxy.
	for op, isOp := errors.AsType[*net.OpError](err); isOp; op, isOp = errors.AsType[*net.OpError](op.Err) {
		if op.Op != "dial" {
			continue
		}
		syscallError, isSyscall := errors.AsType[*os.SyscallError](op.Err)
		address, isTCP := op.Addr.(*net.TCPAddr)
		// Go names the Windows connect call ConnectEx ("connectex"); libuv reports it as connect there too.
		if !isSyscall || (syscallError.Syscall != "connect" && syscallError.Syscall != "connectex") || !isTCP {
			return "", false
		}
		code := nodeerrno.ErrorCode(syscallError)
		if code == "" {
			return "", false
		}
		// Node joins the address and port without IPv6 brackets.
		return "connect " + code + " " + address.IP.String() + ":" + strconv.Itoa(address.Port), true
	}
	return "", false
}
