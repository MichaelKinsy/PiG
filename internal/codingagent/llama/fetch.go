package llama

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"time"
	"unicode"
)

// requestTimeout mirrors the AbortSignal.timeout(15_000) both upstream clients
// attach to every JSON request.
const requestTimeout = 15 * time.Second

// timeoutError mirrors the DOMException("TimeoutError") that
// AbortSignal.timeout rejects with. Its text keeps "timeout", which
// index.ts isConnectionError matches.
type timeoutError struct{}

func (timeoutError) Error() string { return "The operation was aborted due to timeout" }

// fetchError mirrors undici's TypeError("fetch failed") for transport
// failures, so connection errors keep the text index.ts matches.
type fetchError struct{ cause error }

func (e *fetchError) Error() string { return "fetch failed" }
func (e *fetchError) Unwrap() error { return e.cause }

// fetchResponse is the part of a fetch Response the clients read: the status
// and the body parsed as JSON, or nil when the body is not JSON.
type fetchResponse struct {
	status  int
	header  http.Header
	payload any
}

func (r fetchResponse) ok() bool { return r.status >= 200 && r.status <= 299 }

// fetchJSON performs one request under the upstream 15 s timeout combined with
// ctx, then parses the body like `await response.json()` inside try/catch.
func fetchJSON(ctx context.Context, method, url string, headers http.Header, body []byte) (fetchResponse, error) {
	requestCtx, cancel := context.WithTimeoutCause(ctx, requestTimeout, timeoutError{})
	defer cancel()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	request, err := http.NewRequestWithContext(requestCtx, method, url, reader)
	if err != nil {
		return fetchResponse{}, &fetchError{cause: err}
	}
	request.Header = headers
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fetchResponse{}, fetchFailure(ctx, requestCtx, err)
	}
	defer func() { _ = response.Body.Close() }()
	result := fetchResponse{status: response.StatusCode, header: response.Header}
	if data, readErr := io.ReadAll(response.Body); readErr == nil {
		var payload any
		if json.Unmarshal(data, &payload) == nil {
			result.payload = payload
		}
	}
	return result, nil
}

// openStream starts a streaming GET. onSent runs once the request is written
// or the attempt ends, so a caller can order a later request after it the way
// upstream's synchronous fetch() call does.
func openStream(ctx context.Context, url string, headers http.Header, onSent func()) (*http.Response, error) {
	sent := onceFunc(onSent)
	defer sent()
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { sent() }}
	request, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, url, nil)
	if err != nil {
		return nil, &fetchError{cause: err}
	}
	request.Header = headers
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, fetchFailure(ctx, ctx, err)
	}
	return response, nil
}

func onceFunc(fn func()) func() {
	if fn == nil {
		return func() {}
	}
	return sync.OnceFunc(fn)
}

// fetchFailure maps a transport error to the rejection fetch produces: the
// caller's abort reason, the timeout, or "fetch failed".
func fetchFailure(parent, requestCtx context.Context, err error) error {
	if parent.Err() != nil {
		return context.Cause(parent)
	}
	if requestCtx.Err() != nil {
		return context.Cause(requestCtx)
	}
	return &fetchError{cause: err}
}

// abortReason mirrors `signal.reason ?? new Error("Cancelled")`.
func abortReason(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return errors.New("Cancelled")
}

// sleep mirrors client.ts sleep: it resolves after d or rejects with the
// abort reason.
func sleep(ctx context.Context, d time.Duration) error {
	if ctx.Err() != nil {
		return abortReason(ctx)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return abortReason(ctx)
	}
}

// objectField reads key from a decoded JSON object, mirroring
// `(value as { key?: unknown }).key` with a non-object guard.
func objectField(value any, key string) (any, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	field, present := object[key]
	return field, present
}

func stringField(value any, key string) (string, bool) {
	field, _ := objectField(value, key)
	text, ok := field.(string)
	return text, ok
}

// objectValues mirrors Object.values for a decoded JSON object or array; ok is
// false for values `typeof x === "object"` rejects.
func objectValues(value any) ([]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		values := make([]any, 0, len(typed))
		for _, entry := range typed {
			values = append(values, entry)
		}
		return values, true
	case []any:
		return typed, true
	}
	return nil, false
}

// jsTruthy mirrors JavaScript truthiness for a decoded JSON value.
func jsTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case float64:
		return typed != 0
	case string:
		return typed != ""
	}
	return true
}

func numberField(value any, key string) (float64, bool) {
	field, _ := objectField(value, key)
	number, ok := field.(float64)
	return number, ok
}

// formEncode mirrors URLSearchParams serialization for ordered pairs.
func formEncode(pairs ...[2]string) string {
	var b strings.Builder
	for index, pair := range pairs {
		if index > 0 {
			b.WriteByte('&')
		}
		b.WriteString(percentEncode(pair[0], "*-._", true))
		b.WriteByte('=')
		b.WriteString(percentEncode(pair[1], "*-._", true))
	}
	return b.String()
}

func percentEncode(value, safe string, spaceAsPlus bool) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for _, c := range []byte(strings.ToValidUTF8(value, "\uFFFD")) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', strings.IndexByte(safe, c) >= 0:
			b.WriteByte(c)
		case c == ' ' && spaceAsPlus:
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}

// isJSWhitespace mirrors the characters String.prototype.trim removes.
func isJSWhitespace(r rune) bool { return unicode.IsSpace(r) || r == '\uFEFF' }

// jsToFixed mirrors Number.prototype.toFixed for a finite, non-negative value:
// the exact binary value is rounded and an exact tie rounds up.
func jsToFixed(value float64, digits int) string {
	scale := new(big.Float).SetPrec(256).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil))
	scaled := new(big.Float).SetPrec(256).Mul(new(big.Float).SetPrec(0).SetFloat64(value), scale)
	whole, _ := scaled.Int(nil)
	fraction := new(big.Float).SetPrec(256).Sub(scaled, new(big.Float).SetInt(whole))
	if fraction.Cmp(big.NewFloat(0.5)) >= 0 {
		whole.Add(whole, big.NewInt(1))
	}
	text := whole.String()
	if digits == 0 {
		return text
	}
	for len(text) <= digits {
		text = "0" + text
	}
	return text[:len(text)-digits] + "." + text[len(text)-digits:]
}
