package ai

// Ports packages/ai/src/utils/error-body.ts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"unicode"

	"github.com/MichaelKinsy/PiG/internal/jsonstringify"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/internal/nodeerrno"
)

const MaxProviderErrorBodyChars = 4000

// NormalizedProviderError separates a provider's HTTP status and body from its error message.
type NormalizedProviderError struct {
	Status             *int    `json:"status,omitempty"`
	Body               *string `json:"body,omitempty"`
	Message            string  `json:"message"`
	MessageCarriesBody bool    `json:"messageCarriesBody"`
}

// providerError carries the raw error fields owned by the HTTP adapters. Native SDK errors retain their own types.
type providerError struct {
	status  *int
	body    any
	message string
	prefix  []string
}

// ProviderStatus is the HTTP status of the response the error reports; a retry policy reads it.
func (err *providerError) ProviderStatus() *int { return err.status }

func (err *providerError) Error() string {
	return FormatProviderError(NormalizeProviderError(err), err.prefix...)
}

// NormalizeProviderError extracts supported error metadata without serializing unread response streams or SDK wrapper objects.
func NormalizeProviderError(value any) NormalizedProviderError {
	if own, ok := value.(*providerError); ok {
		if own == nil {
			return NormalizedProviderError{Message: "null"}
		}
		body := providerErrorBody(own.body)
		status := own.status
		if status != nil {
			status = new(*status)
		}
		return NormalizedProviderError{Status: status, Body: body, Message: own.message, MessageCarriesBody: body == nil || strings.Contains(own.message, *body)}
	}
	err, ok := value.(error)
	if !ok {
		return NormalizedProviderError{Message: SafeJsonStringify(value)}
	}
	result := NormalizedProviderError{Message: err.Error(), MessageCarriesBody: true}
	// pig additive (D92): only a provider package that links an SDK (ai/providers/bedrock, the AWS SDK) produces a status-carrying response error; a program without it has none.
	if status, ok := responseErrorStatus(err); ok {
		result.Status = new(status)
	}
	if result.Status == nil {
		result.Message = WithCauseDetail(err)
	}
	return result
}

// maxCauseDepth is how many causes WithCauseDetail follows.
// upstream: packages/ai/src/utils/error-body.ts:MAX_CAUSE_DEPTH
const maxCauseDepth = 5

// WithCauseDetail is error-body.ts withCauseDetail. Connection-level failures carry no status or body, and an SDK replaces the reason
// with a generic message; the reason sits in the cause chain (errors.Unwrap here). It appends the deepest cause's message, and its
// code when the error has a Code method and the message lacks it, so callers that surface only the message keep the reason.
//
// An error that passed through the Node fetch boundary (nodeTransportError) carries a Go cause for diagnostics, not the cause Node's
// undici attaches: a "fetch failed" gets the Node message of the connection failure, and an abort or a dropped body has no detail.
func WithCauseDetail(err error) string {
	message := err.Error()
	var detail, code string
	if transport, ok := errors.AsType[*nodeTransportError](err); ok {
		// An SDK's "Connection error." wraps the "fetch failed" rejection whose cause names the failure.
		if transport.message == "Connection error." {
			if inner, ok := errors.AsType[*nodeTransportError](transport.cause); ok {
				transport = inner
			}
		}
		switch transport.message {
		case "fetch failed":
			detail, code = nodeConnectionFailure(transport.cause)
		case "terminated":
			detail, code = nodeBodyFailure(transport.cause)
		}
		if detail == "" {
			return message
		}
	} else {
		var deepest error
		for current, depth := nodeCause(err), 0; depth < maxCauseDepth && current != nil; depth++ {
			deepest = current
			current = nodeCause(current)
		}
		if deepest == nil {
			return message
		}
		detail = trimJSWhitespace(deepest.Error())
		if detail == "" {
			detail = "Error"
		}
		if coded, ok := deepest.(interface{ Code() string }); ok {
			code = coded.Code()
		}
	}
	if code != "" && !strings.Contains(detail, code) {
		detail += " (" + code + ")"
	}
	if strings.Contains(message, detail) {
		return message
	}
	if message == "" {
		return detail
	}
	return message + ": " + detail
}

// nodeCause is err's `cause` as Node has it. A Node fs error (nodeerrno.FSError) has none: its Unwrap is the Go error behind it, which
// names the failure in the platform's words ("The system cannot find the file specified." for Windows' ERROR_FILE_NOT_FOUND) and is
// not part of Node's message, which the FSError already is.
func nodeCause(err error) error {
	if _, fs := err.(*nodeerrno.FSError); fs { //nolint:errorlint // Only the fs error itself ends the chain; one that wraps it has the cause.
		return nil
	}
	return errors.Unwrap(err)
}

func providerErrorBody(value any) *string {
	var text string
	switch value := value.(type) {
	case string:
		text = value
	case json.RawMessage:
		var object map[string]json.RawMessage
		if json.Unmarshal(value, &object) != nil || len(object) == 0 {
			return nil
		}
		text = SafeJsonStringify(value)
	default:
		if value == nil {
			return nil
		}
		reflected := reflect.ValueOf(value)
		if reflected.Kind() != reflect.Map || reflected.Type().Key().Kind() != reflect.String || reflected.Len() == 0 {
			return nil
		}
		text = SafeJsonStringify(value)
	}
	text = strings.TrimFunc(text, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
	if text == "" {
		return nil
	}
	return new(TruncateErrorText(text, MaxProviderErrorBodyChars))
}

// FormatProviderError adds body/status only when they are not already carried by the message. Omit prefix to return the unprefixed provider error.
func FormatProviderError(norm NormalizedProviderError, prefix ...string) string {
	message := norm.Message
	if !norm.MessageCarriesBody && norm.Status != nil && norm.Body != nil {
		message = *norm.Body
	}
	if len(prefix) > 0 && norm.Status != nil {
		return fmt.Sprintf("%s (%d): %s", prefix[0], *norm.Status, message)
	}
	if !norm.MessageCarriesBody && norm.Status != nil && norm.Body != nil {
		return fmt.Sprintf("%d: %s", *norm.Status, message)
	}
	return message
}

// TruncateErrorText caps error text in UTF-16 units and reports the number of omitted units.
func TruncateErrorText(text string, maxChars int) string {
	length := utf16Length(text)
	if length <= maxChars {
		return text
	}
	return fmt.Sprintf("%s... [truncated %d chars]", truncateUTF16(text, maxChars), length-maxChars)
}

// SafeJsonStringify formats JSON-compatible values without HTML escaping and falls back to their Go string representation when encoding fails.
func SafeJsonStringify(value any) string {
	state := safeJSONEncoders.Get().(*safeJSONEncoder)
	defer state.release()
	return string(state.stringify(value))
}

// appendSafeJsonStringify appends SafeJsonStringify(value) to dst without building the string.
func appendSafeJsonStringify(dst []byte, value any) []byte {
	state := safeJSONEncoders.Get().(*safeJSONEncoder)
	defer state.release()
	return append(dst, state.stringify(value)...)
}

// maxPooledSafeJSONBuffer bounds the buffers a pooled safeJSONEncoder keeps (golang/go#23199): one large tool argument must not pin them.
// pig additive (D112): Pi keeps no encoder between JSON.stringify calls; a state that is not pooled is made again and stringifies the same.
const maxPooledSafeJSONBuffer = 64 << 10

// release returns state to the pool unless its buffers grew past maxPooledSafeJSONBuffer; the pool makes a fresh state when it is empty.
func (state *safeJSONEncoder) release() {
	if state.buffer.Cap() > maxPooledSafeJSONBuffer || cap(state.canonical) > maxPooledSafeJSONBuffer {
		return
	}
	safeJSONEncoders.Put(state)
}

// safeJSONEncoder keeps an encoder and its buffers between calls: the transcript estimate of the faux provider stringifies every tool call of the whole history on every model call.
type safeJSONEncoder struct {
	buffer    bytes.Buffer
	encoder   *json.Encoder
	canonical []byte
}

var safeJSONEncoders = sync.Pool{New: func() any {
	state := &safeJSONEncoder{}
	state.encoder = json.NewEncoder(&state.buffer)
	state.encoder.SetEscapeHTML(false)
	return state
}}

// stringify is SafeJsonStringify(value); the result is valid until the encoder's next call.
func (state *safeJSONEncoder) stringify(value any) []byte {
	state.buffer.Reset()
	if err := state.encoder.Encode(value); err != nil {
		// encoding/json refuses a NaN or an infinity, which JSON.stringify writes as null: only a value with no JSON form is written as String(value).
		if stringified, stringifyErr := jsstring.MarshalJSON(value); stringifyErr == nil {
			return append(state.canonical[:0], stringified...)
		}
		return append(state.canonical[:0], fmt.Sprint(value)...)
	}
	encoded := bytes.TrimSuffix(state.buffer.Bytes(), []byte("\n"))
	// JSON.stringify writes U+2028 and U+2029 raw, -0 as 0, numbers in ECMAScript form and integer keys first; encoding/json differs on each.
	canonical, err := jsonstringify.AppendCanonical(state.canonical[:0], encoded)
	if err != nil {
		return encoded
	}
	state.canonical = canonical
	return canonical
}

// nodeBodyFailure is the message and code of the cause undici attaches to "terminated" for a response body that failed after the headers:
// SocketError `other side closed` (UND_ERR_SOCKET) for a connection the peer closed or reset, BodyTimeoutError (UND_ERR_BODY_TIMEOUT) for an idle body.
// It is empty for a failure with no such Node counterpart.
func nodeBodyFailure(cause error) (message, code string) {
	if idle, ok := errors.AsType[*sseIdleError](cause); ok {
		return idle.Error(), ""
	}
	if timeout, ok := errors.AsType[net.Error](cause); ok && timeout.Timeout() {
		return "Body Timeout Error", "UND_ERR_BODY_TIMEOUT"
	}
	if errors.Is(cause, io.ErrUnexpectedEOF) || errors.Is(cause, io.EOF) || errors.Is(cause, syscall.ECONNRESET) || errors.Is(cause, syscall.EPIPE) || errors.Is(cause, net.ErrClosed) {
		return "other side closed", "UND_ERR_SOCKET"
	}
	return "", ""
}

// nodeConnectionFailure is the message and code of the cause undici attaches to "fetch failed" for a failed connection: Node's
// `connect ECONNREFUSED 127.0.0.1:8080` for a refused or unreachable dial and `getaddrinfo ENOTFOUND host` for a name that does not
// resolve, and the socket errors undici's connection reports before the response headers: `write ECONNRESET` and `write EPIPE` for a request
// the peer reset while it was written, `read ECONNRESET` for a reset while the response was awaited, and SocketError `other side closed`
// (UND_ERR_SOCKET) for a connection the peer closed. It is empty for a failure with no such Node counterpart.
//
// A peer that resets on accept is reported by Node mostly as `connect ECONNRESET <addr>` (libuv sees the reset before the connect completes) but by Go
// as `read ECONNRESET`, because Go's dial returns first and the request write lands in the socket buffer; D520 records the difference.
func nodeConnectionFailure(cause error) (message, code string) {
	if errors.Is(cause, errFetchBadPort) {
		return "bad port", ""
	}
	if dns, ok := errors.AsType[*net.DNSError](cause); ok && dns.IsNotFound {
		return "getaddrinfo ENOTFOUND " + dns.Name, "ENOTFOUND"
	}
	if op, ok := errors.AsType[*net.OpError](cause); ok {
		switch {
		case op.Op == "dial" && op.Addr != nil:
			if code = nodeerrno.ErrorCode(op.Err); code != "" {
				return "connect " + code + " " + op.Addr.String(), code
			}
		// Node's read and write errors name the syscall and the code, not an address.
		// pig divergence (D520): a reset on accept reaches Go's response read, where Node mostly reports it on the connect.
		case op.Op == "write" && (errors.Is(op.Err, syscall.ECONNRESET) || errors.Is(op.Err, syscall.EPIPE)), op.Op == "read" && errors.Is(op.Err, syscall.ECONNRESET):
			code = nodeerrno.ErrorCode(op.Err)
			return op.Op + " " + code, code
		}
	}
	if errors.Is(cause, io.ErrUnexpectedEOF) || errors.Is(cause, io.EOF) || errors.Is(cause, net.ErrClosed) || isServerClosedIdle(cause) {
		return "other side closed", "UND_ERR_SOCKET"
	}
	return "", ""
}

// isServerClosedIdle reports net/http's unexported errServerClosedIdle, the error a transport gives a request whose connection the server
// closed before the request's first byte was read: a peer's FIN that the connection's reader sees before the request's own write or read does.
func isServerClosedIdle(err error) bool {
	//portlint:allow erroridentity net/http exports no value or type for errServerClosedIdle (transport.go), so its text is its identity
	return err != nil && err.Error() == "http: server closed idle connection"
}
