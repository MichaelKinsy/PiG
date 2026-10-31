package http

import (
	"fmt"
	"io"
	"iter"
	nethttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/src/next-node/http/request.ts. Requests (slice 10 §2, D4): what a route, a wrap, a socket handler and
// an authenticator see of an HTTP request (once admitted), and its body, read only when asked: `JSON(limit)` (1 MiB by
// default, malformed 400, too large 413) and `Body(limit)`, a sequence of chunks.
//
// Forced Go differences. `Headers` is a net/http.Header (with `Host` in it; HeaderGet reads one as `Headers.get` does),
// `URLSearchParams` is url.Values, `Record<string, string>` params a map. `limit?: number` is an optional variadic
// argument. The async iterable `body` is an iter.Seq2 of chunks and the error a `for await` would throw. A `throw` of
// HttpFailure is a returned *HttpFailure.

// HttpRequest is a request as routes see it, once admitted: `Origin` and `Provenance` come only from admission.
type HttpRequest interface {
	Method() string
	// Path is the path, without the query, percent-encoded, its dot segments resolved (by the URL parser).
	Path() string
	// Params are the matched pattern's params, percent-decoded (`*` for the rest of a `/*` pattern).
	Params() map[string]string
	Query() url.Values
	// Headers are the request's headers; read only.
	Headers() nethttp.Header
	// Origin is `http://<host>` on loopback, `https://<host>` for an allowed host, a relay's origin for a relayed request.
	Origin() string
	// Provenance is where it came from (decision "The HTTP component's security baseline" (1), "Relays").
	Provenance() Provenance
	// Nonce is the server's script nonce: a page's inline scripts carry it (`<script nonce="…">`), the CSP allows them.
	Nonce() string
	// JSON is the body as JSON, at most limit bytes (1 MiB by default): *HttpFailure 400 when malformed, 413 when larger.
	JSON(limit ...int) (any, error)
	// Body is the body's chunks, at most limit bytes (1 MiB by default): *HttpFailure 413 past it, 400 when cut short.
	Body(limit ...int) iter.Seq2[[]byte, error]
}

// HttpFailure is a failure that answers its request with Status when it escapes a route (400 malformed, 413 too large).
type HttpFailure struct {
	Status  int
	Message string
}

// NewHttpFailure is `new HttpFailure(status, message)`.
func NewHttpFailure(status int, message string) *HttpFailure {
	return &HttpFailure{Status: status, Message: message}
}

func (f *HttpFailure) Error() string { return f.Message }

// defaultLimit is what `JSON` and `Body` read unless a route says otherwise: 1 MiB.
const defaultLimit = 1024 * 1024

// webRequest is a request over what the component reads of it; the component sets its params once it matched a route.
type webRequest struct {
	method     string
	path       string
	params     map[string]string
	origin     string
	provenance Provenance
	nonce      string
	incoming   Incoming
	// search is the query string (without `?`), parsed when first read.
	search string
	query  url.Values
	mu     sync.Mutex
	// taken is whether a reader took the body (it is read once).
	taken bool
}

var _ HttpRequest = (*webRequest)(nil)

func newWebRequest(incoming Incoming, admitted *Admitted, nonce string) *webRequest {
	// The URL is parsed and serialized already (dot segments resolved, `\` as `/`): only split it here.
	path, search := splitURL(incoming.URL())
	return &webRequest{
		method: incoming.Method(), path: path, search: search, origin: admitted.Origin,
		provenance: admitted.Provenance, nonce: nonce, incoming: incoming, params: map[string]string{},
	}
}

func (r *webRequest) Method() string            { return r.method }
func (r *webRequest) Path() string              { return r.path }
func (r *webRequest) Params() map[string]string { return r.params }
func (r *webRequest) Origin() string            { return r.origin }
func (r *webRequest) Provenance() Provenance    { return r.provenance }
func (r *webRequest) Nonce() string             { return r.nonce }

func (r *webRequest) Headers() nethttp.Header { return requestHeaders(r.incoming.Request()) }

func (r *webRequest) Query() url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.query == nil {
		r.query, _ = url.ParseQuery(r.search)
		if r.query == nil {
			r.query = url.Values{}
		}
	}
	return r.query
}

func (r *webRequest) JSON(limit ...int) (any, error) {
	var text strings.Builder
	for chunk, err := range r.Body(limit...) {
		if err != nil {
			return nil, err
		}
		text.Write(chunk)
	}
	value, err := delta.DecodeJson([]byte(text.String()))
	if err != nil {
		return nil, NewHttpFailure(400, "the body is no JSON")
	}
	return value, nil
}

func (r *webRequest) Body(limit ...int) iter.Seq2[[]byte, error] {
	max := defaultLimit
	if len(limit) > 0 {
		max = limit[0]
	}
	return func(yield func([]byte, error) bool) {
		r.mu.Lock()
		taken := r.taken
		r.taken = true
		r.mu.Unlock()
		if taken {
			yield(nil, fmt.Errorf("request body: read twice"))
			return
		}
		if declared, ok := r.incoming.Header("content-length"); ok {
			if length, ok := parseNumber(declared); ok && length > float64(max) {
				yield(nil, NewHttpFailure(413, fmt.Sprintf("the body is larger than %d bytes", max)))
				return
			}
		}
		body := r.incoming.Request().Body
		if body == nil || body == nethttp.NoBody {
			return
		}
		read := 0
		buffer := make([]byte, 32*1024)
		for {
			n, err := body.Read(buffer)
			if n > 0 {
				read += n
				if read > max {
					yield(nil, NewHttpFailure(413, fmt.Sprintf("the body is larger than %d bytes", max)))
					return
				}
				if !yield(append([]byte(nil), buffer[:n]...), nil) {
					return
				}
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				// A body cut short (a disconnect, a broken stream) is no body: a prefix must never parse as the whole.
				yield(nil, NewHttpFailure(400, "the body could not be read whole"))
				return
			}
		}
	}
}

// parseNumber is JavaScript's Number(text) for a header's digits: false for NaN.
func parseNumber(text string) (float64, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, true
	}
	value, err := strconv.ParseFloat(text, 64)
	return value, err == nil
}

// splitURL is a serialized URL's path (from the first `/` after the authority) and query (without `?`, no fragment).
func splitURL(raw string) (path, search string) {
	authority := strings.Index(raw, "//") + 2
	start := strings.Index(raw[authority:], "/")
	if start >= 0 {
		start += authority
	}
	end := strings.Index(raw[authority:], "#")
	if end < 0 {
		end = len(raw)
	} else {
		end += authority
	}
	mark := strings.Index(raw[authority:], "?")
	if mark >= 0 {
		mark += authority
	}
	if start < 0 || (mark >= 0 && start > mark) {
		start = -1
	}
	pathEnd := end
	if mark >= 0 && mark < end {
		pathEnd = mark
	}
	if start < 0 {
		path = "/"
	} else {
		path = raw[start:pathEnd]
	}
	if mark >= 0 && mark < end {
		search = raw[mark+1 : end]
	}
	return path, search
}
