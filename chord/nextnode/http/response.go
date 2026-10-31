package http

import (
	"context"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/next/host"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports packages/chord/src/next-node/http/response.ts. Responses (slice 10 §2, D5, R6; decision "The HTTP component's
// security baseline" (4), (6)): routes return descriptions and the component turns them into an answer, a status, headers
// and a body: the default headers first, the response's own replacing same-named ones (validated: a CR or LF is an error),
// `json`, `html`, `redirect`, `error`, and `bytes` read in 256 KiB pieces as the consumer pulls them (ranges 206/416,
// `HEAD`, an attachment unless inline, safe download names; without a `size`, until an empty read).
//
// Forced Go differences.
//   - The HttpResponse union is one struct tagged by Kind; optional numbers and strings are pointers or their zero values
//     where Pi's `undefined` and the zero value mean the same (Status 0, Download "", To ""). Headers are an ordered
//     slice of (name, values) pairs.
//   - A `bytes` response's `read` blocks and returns (bytes, error).
//   - A Web `ReadableStream` body is a *PulledBody, an io.ReadCloser that pulls one piece per Read; Closed reports its end.
//   - `STATUS_CODES` is Node's reason-phrase table (statusText below): Go's differs for some statuses.

// ResponseKind tells what an HttpResponse describes.
type ResponseKind string

const (
	KindJSON     ResponseKind = "json"
	KindHTML     ResponseKind = "html"
	KindBytes    ResponseKind = "bytes"
	KindRedirect ResponseKind = "redirect"
	KindError    ResponseKind = "error"
)

// ResponseHeader is one header of a response: a list sends one line per value (several `Set-Cookie`).
type ResponseHeader struct {
	Name   string
	Values []string
}

// HeaderOf is a response header of one or more values.
func HeaderOf(name string, values ...string) ResponseHeader {
	return ResponseHeader{Name: name, Values: values}
}

// HttpResponse is a route's answer, as a description; Headers replace same-named default ones (a route widens its own
// CSP); a list sends one line per value.
type HttpResponse struct {
	Kind    ResponseKind
	Headers []ResponseHeader
	// Status is the status of json, html, redirect (302, 303 or 307) and error; 0 is the default (200, 302).
	Status int
	// Value is the JSON value of a json response.
	Value any
	// Body is the text of an html response.
	Body string
	// Mime is the type of a bytes response.
	Mime string
	// Size is the length of a bytes response: `Content-Length` and ranges; nil: read until `Read` returns an empty chunk.
	Size *int64
	// Read is `length` bytes from `offset` (fewer only at the end of a body without Size), in the request's scope.
	Read func(offset int64, length int, ctx host.Ctx) ([]byte, error)
	// Download is the file name a bytes response is saved under.
	Download string
	// Inline is shown in the browser instead of saved (an image, a video), with a `sandbox` CSP.
	Inline bool
	// Ranges is whether a single range is answered 206 (with Size).
	Ranges bool
	// To is the target of a redirect.
	To string
	// Message is an error response's text; nil: the status's reason phrase.
	Message *string
}

// Answer is what the component answers: a status, its headers, and a whole body (Text, UTF-8), a pulled one (Stream), or
// none (both empty).
type Answer struct {
	Status  int
	Headers *AnswerHeaders
	Text    string
	Stream  *PulledBody
}

// ResponseFailure is a `bytes` body that could not be read whole (a failing or a short `read`).
type ResponseFailure struct {
	Message string
	Cause   error
}

func (e *ResponseFailure) Error() string { return e.Message }
func (e *ResponseFailure) Unwrap() error { return e.Cause }

// answerOf is the answer to request for response over defaults (which it changes): it returns an error when a header is
// invalid (a CR or LF), the value is no JSON or the status is out of range. A `bytes` body that cannot be read whole is
// reported to failed (its stream errors).
func answerOf(response HttpResponse, request Incoming, defaults *AnswerHeaders, ctx host.Ctx, failed func(error)) (Answer, error) {
	headers := defaults
	for _, header := range response.Headers {
		if err := headers.Set(header.Name, header.Values...); err != nil {
			return Answer{}, err
		}
	}
	head := request.Method() == "HEAD"
	switch response.Kind {
	case KindJSON:
		text, err := chordjson.Marshal(response.Value)
		if err != nil {
			return Answer{}, err
		}
		return whole(statusOr(response.Status, 200), headers, "application/json; charset=utf-8", string(text), head)
	case KindHTML:
		return whole(statusOr(response.Status, 200), headers, "text/html; charset=utf-8", response.Body, head)
	case KindRedirect:
		headers.mustSet("Location", response.To)
		return whole(statusOr(response.Status, 302), headers, "", "", head)
	case KindError:
		message := ""
		if response.Message != nil {
			message = *response.Message
		} else {
			message = statusText[response.Status]
		}
		return whole(response.Status, headers, "text/plain; charset=utf-8", message, head)
	case KindBytes:
		return bytesAnswer(response, request, headers, ctx, failed)
	}
	return Answer{}, fmt.Errorf("response: kind %q", response.Kind)
}

func statusOr(status, fallback int) int {
	if status == 0 {
		return fallback
	}
	return status
}

// errorAnswer is the answer for an error status with its reason phrase (or message) as plain text.
func errorAnswer(status int, headers *AnswerHeaders, message *string, head bool) Answer {
	text := statusText[status]
	if message != nil {
		text = *message
	}
	answer, err := whole(status, headers, "text/plain; charset=utf-8", text, head)
	if err != nil {
		panic(err)
	}
	return answer
}

// whole is a whole body: type unless a header set one, and its length; none for `HEAD`, and neither for a status that
// carries no body (204, 205, 304).
func whole(status int, headers *AnswerHeaders, contentType string, text string, head bool) (Answer, error) {
	if status < 200 || status > 599 {
		return Answer{}, fmt.Errorf("response: status %d", status)
	}
	if bodyless[status] {
		headers.Delete("Content-Type")
		headers.Delete("Content-Length")
		return Answer{Status: status, Headers: headers}, nil
	}
	if contentType != "" && !headers.Has("content-type") {
		headers.mustSet("Content-Type", contentType)
	}
	headers.mustSet("Content-Length", strconv.Itoa(len(text)))
	if head || text == "" {
		text = ""
	}
	return Answer{Status: status, Headers: headers, Text: text}, nil
}

// bodyless are the statuses whose responses carry no body.
var bodyless = map[int]bool{204: true, 205: true, 304: true}

// piece is one `read` of a `bytes` body: 256 KiB.
const piece = 256 * 1024

// bytesAnswer is a `bytes` answer: its range or all of it, or until an empty read without a size.
func bytesAnswer(response HttpResponse, request Incoming, headers *AnswerHeaders, ctx host.Ctx, failed func(error)) (Answer, error) {
	if !headers.Has("content-type") {
		if err := headers.Set("Content-Type", response.Mime); err != nil {
			return Answer{}, err
		}
	}
	headers.mustSet("Content-Disposition", disposition(response))
	if response.Inline {
		current, _ := headers.Get("content-security-policy")
		headers.mustSet("Content-Security-Policy", current+"; sandbox")
	}
	var start int64
	var length *int64
	if response.Size != nil {
		size := *response.Size
		length = &size
	}
	status := 200
	if response.Size != nil && response.Ranges {
		size := *response.Size
		headers.mustSet("Accept-Ranges", "bytes")
		rangeHeader, hasRange := request.Header("range")
		if !hasRange {
			rangeHeader = ""
		}
		r, kind := rangeOf(rangeHeader, hasRange, size)
		switch kind {
		case rangeUnsatisfiable:
			headers.mustSet("Content-Range", fmt.Sprintf("bytes */%d", size))
			return whole(416, headers, "", "", true)
		case rangeSingle:
			start = r.start
			rangeLength := r.length
			length = &rangeLength
			status = 206
			headers.mustSet("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+r.length-1, size))
		}
	}
	if length != nil {
		headers.mustSet("Content-Length", strconv.FormatInt(*length, 10))
	}
	if request.Method() == "HEAD" || (length != nil && *length == 0) {
		return Answer{Status: status, Headers: headers}, nil
	}
	return Answer{Status: status, Headers: headers, Stream: pulled(response, start, length, ctx, failed)}, nil
}

// PulledBody is the body of a `bytes` response from `start`, `length` bytes (nil: until an empty read), read one piece per
// Read, so the consumer's backpressure paces the reads; a `read` that fails or comes back short fails it. It follows the
// request's scope: once sealed (a disconnect, `close`) the stream errors, no read starts, a late one is dropped.
type PulledBody struct {
	response HttpResponse
	ctx      host.Ctx
	failed   func(error)
	end      int64
	unbound  bool

	mu     sync.Mutex
	offset int64
	// pending is the part of the last piece the consumer has not taken.
	pending []byte
	// last is whether the piece in pending is the last one.
	last bool
	// err is how the stream ended: io.EOF or an error; nil while it goes on.
	err    error
	closed chan struct{}
	stop   func() bool
}

func pulled(response HttpResponse, start int64, length *int64, ctx host.Ctx, failed func(error)) *PulledBody {
	body := &PulledBody{response: response, ctx: ctx, failed: failed, offset: start, closed: make(chan struct{})}
	if length == nil {
		body.unbound = true
	} else {
		body.end = start + *length
	}
	// Sealed before the stream exists (an abort while the route returned it): no event would come after; an abort later
	// errors the stream, which ends the request even if its consumer never reads again.
	if ctx.Done() != nil {
		if ctx.Err() != nil {
			body.finish(context.Cause(ctx))
		} else {
			body.mu.Lock()
			body.stop = context.AfterFunc(ctx, func() { body.finish(context.Cause(ctx)) })
			body.mu.Unlock()
		}
	}
	return body
}

// Closed is closed once the stream ended: read to its end, failed, cancelled or errored by the scope's seal.
func (b *PulledBody) Closed() <-chan struct{} { return b.closed }

// finish ends the stream with err (io.EOF for a whole read); the first end wins.
func (b *PulledBody) finish(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finishLocked(err)
}

func (b *PulledBody) finishLocked(err error) {
	if b.err != nil {
		return
	}
	b.err = err
	b.pending = nil
	if b.stop != nil {
		b.stop()
	}
	close(b.closed)
}

// Read pulls the next piece when the last one is taken.
func (b *PulledBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if len(b.pending) == 0 && b.err == nil {
		if b.last {
			b.finishLocked(io.EOF)
		} else {
			b.mu.Unlock()
			b.pull()
			b.mu.Lock()
		}
	}
	defer b.mu.Unlock()
	if len(b.pending) > 0 {
		n := copy(p, b.pending)
		b.pending = b.pending[n:]
		return n, nil
	}
	if b.err != nil {
		return 0, b.err
	}
	return 0, nil
}

// pull reads the next piece (outside the lock: the read is the route's).
func (b *PulledBody) pull() {
	ctx := b.ctx
	if ctx.Err() != nil {
		b.finish(context.Cause(ctx))
		return
	}
	b.mu.Lock()
	offset := b.offset
	b.mu.Unlock()
	want := piece
	if !b.unbound {
		if left := b.end - offset; left < int64(want) {
			want = int(left)
		}
	}
	type result struct {
		chunk []byte
		err   error
	}
	done := make(chan result, 1)
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				done <- result{err: fmt.Errorf("%v", recovered)}
			}
		}()
		chunk, err := b.response.Read(offset, want, ctx)
		if err == nil && !b.unbound && len(chunk) != want {
			err = &ResponseFailure{Message: fmt.Sprintf("bytes: read %d of %d bytes at %d (size %s)", len(chunk), want, offset, sizeText(b.response.Size))}
		}
		done <- result{chunk, err}
	}()
	var outcome result
	select {
	case outcome = <-done:
	case <-ctx.Done():
		// The seal errors the stream; the late chunk is dropped.
		b.finish(context.Cause(ctx))
		return
	}
	if outcome.err != nil {
		// A read cut by the seal is no failure of the response.
		if ctx.Err() != nil {
			b.finish(context.Cause(ctx))
			return
		}
		b.failed(outcome.err)
		b.finish(outcome.err)
		return
	}
	if ctx.Err() != nil {
		b.finish(context.Cause(ctx))
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return
	}
	if len(outcome.chunk) == 0 || (!b.unbound && offset+int64(len(outcome.chunk)) >= b.end) {
		b.pending = outcome.chunk
		b.last = true
		if len(outcome.chunk) == 0 {
			b.finishLocked(io.EOF)
		}
		return
	}
	b.pending = outcome.chunk
	b.offset = offset + int64(len(outcome.chunk))
}

func sizeText(size *int64) string {
	if size == nil {
		return "undefined"
	}
	return strconv.FormatInt(*size, 10)
}

// Close cancels the stream.
func (b *PulledBody) Close() error {
	b.finish(io.ErrClosedPipe)
	return nil
}

type rangeKind int

const (
	rangeNone rangeKind = iota
	rangeSingle
	rangeUnsatisfiable
)

type byteRange struct{ start, length int64 }

var rangePattern = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)

// rangeOf is the single range header asks for within size bytes; none for no header, a malformed or a multi-range one (the
// whole body); unsatisfiable for one starting at or past the end.
func rangeOf(header string, present bool, size int64) (byteRange, rangeKind) {
	if !present {
		return byteRange{}, rangeNone
	}
	match := rangePattern.FindStringSubmatch(strings.TrimSpace(header))
	if match == nil {
		return byteRange{}, rangeNone
	}
	from, to := match[1], match[2]
	if from == "" && to == "" {
		return byteRange{}, rangeNone
	}
	if from == "" {
		suffix, _ := strconv.ParseFloat(to, 64)
		if suffix == 0 || size == 0 {
			return byteRange{}, rangeUnsatisfiable
		}
		length := int64(math.Min(suffix, float64(size)))
		return byteRange{start: size - length, length: length}, rangeSingle
	}
	startValue, _ := strconv.ParseFloat(from, 64)
	if startValue >= float64(size) {
		return byteRange{}, rangeUnsatisfiable
	}
	start := int64(startValue)
	last := size - 1
	if to != "" {
		toValue, _ := strconv.ParseFloat(to, 64)
		last = int64(math.Min(toValue, float64(size-1)))
	}
	if last < start {
		return byteRange{}, rangeNone
	}
	return byteRange{start: start, length: last - start + 1}, rangeSingle
}

// disposition is `Content-Disposition`: `inline` or `attachment`, with the download name as an ASCII fallback and as UTF-8
// (RFC 6266, RFC 5987), its control and bidirectional formatting characters dropped (a name must read as it is).
func disposition(response HttpResponse) string {
	kind := "attachment"
	if response.Inline {
		kind = "inline"
	}
	if response.Download == "" {
		return kind
	}
	var name strings.Builder
	for _, r := range response.Download {
		if r <= 0x1f || r == 0x7f || r == 0x61c || r == 0x200e || r == 0x200f || (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		name.WriteRune(r)
	}
	var ascii strings.Builder
	// JavaScript replaces per UTF-16 code unit: a character above U+FFFF is two underscores.
	for _, r := range name.String() {
		switch {
		case r < 0x20 || r > 0x7e || r == '"' || r == '\\':
			if r > 0xffff {
				ascii.WriteString("__")
			} else {
				ascii.WriteByte('_')
			}
		default:
			ascii.WriteRune(r)
		}
	}
	encoded := encodeURIComponent(name.String())
	var escaped strings.Builder
	for i := 0; i < len(encoded); i++ {
		switch encoded[i] {
		case '\'', '(', ')', '*':
			fmt.Fprintf(&escaped, "%%%02X", encoded[i])
		default:
			escaped.WriteByte(encoded[i])
		}
	}
	return fmt.Sprintf("%s; filename=\"%s\"; filename*=UTF-8''%s", kind, ascii.String(), escaped.String())
}
