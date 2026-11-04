package http

import (
	"context"
	nethttp "net/http"
	"strings"
)

// Ports packages/chord/src/next-node/http/incoming.ts. What the component reads of a request: one shape for a request an
// app's own server hands to Fetch and for Chord's own listener, so admission, routing and the baseline have one
// implementation.
//
// Forced Go differences. Pi's Web `Request` is Go's *net/http.Request, which exists already, so Pi's lazily made Web
// `Request` on the Node side (`request()`) has no counterpart cost: Request returns the request in hand. A header lookup
// joins several values of one name with ", " as `Headers.get` does (HeaderGet); `Host` is read from the `Host` header
// entry when a caller put one there, else from Request.Host, because Go's server removes it from the header map.

// Incoming is a request as the component reads it.
type Incoming interface {
	Method() string
	// URL is the URL as the URL parser serializes it (dot segments resolved, `\` as `/`).
	URL() string
	// Header is a header's value, several of one name joined with ", "; false without it.
	Header(name string) (string, bool)
	// Forwarded is whether a proxy forwarded it: a `Forwarded` or `X-Forwarded-*` header.
	Forwarded() bool
	// Cancelled calls abort once when the client goes away before its answer is written (at once when it has).
	Cancelled(abort func(reason error))
	// Request is the request itself.
	Request() *nethttp.Request
}

// HeaderGet is `Headers.get`: the values of name joined with ", ", false when there is none.
func HeaderGet(headers nethttp.Header, name string) (string, bool) {
	values := headers.Values(name)
	if len(values) == 0 {
		return "", false
	}
	if len(values) == 1 {
		return values[0], true
	}
	return strings.Join(values, ", "), true
}

// webIncoming is Pi's `webIncoming`.
type webIncoming struct {
	request *nethttp.Request
	url     string
}

// WebIncoming is request as the component reads it.
func WebIncoming(request *nethttp.Request) Incoming {
	return &webIncoming{request: request, url: webURL(request)}
}

func (w *webIncoming) Method() string { return w.request.Method }

func (w *webIncoming) URL() string { return w.url }

func (w *webIncoming) Header(name string) (string, bool) { return requestHeader(w.request, name) }

func (w *webIncoming) Forwarded() bool { return forwardedHeaders(w.request.Header) }

func (w *webIncoming) Cancelled(abort func(reason error)) { cancelledBy(w.request.Context(), abort) }

func (w *webIncoming) Request() *nethttp.Request { return w.request }

// requestHeader is a request's header as `Headers.get` gives it, `Host` included.
func requestHeader(request *nethttp.Request, name string) (string, bool) {
	if strings.EqualFold(name, "host") {
		if value, ok := HeaderGet(request.Header, "Host"); ok {
			return value, true
		}
		if request.Host != "" {
			return request.Host, true
		}
		return "", false
	}
	return HeaderGet(request.Header, name)
}

// requestHeaders is a request's headers as a route reads them: its header map, with `Host` in it.
func requestHeaders(request *nethttp.Request) nethttp.Header {
	if _, ok := request.Header["Host"]; ok || request.Host == "" {
		return request.Header
	}
	headers := request.Header.Clone()
	if headers == nil {
		headers = nethttp.Header{}
	}
	headers["Host"] = []string{request.Host}
	return headers
}

// forwardedHeaders is whether header names say a proxy forwarded the request.
func forwardedHeaders(headers nethttp.Header) bool {
	for name := range headers {
		if isForwarding(strings.ToLower(name)) {
			return true
		}
	}
	return false
}

// isForwarding is whether a (lowercased) header name says a proxy forwarded the request.
func isForwarding(name string) bool {
	return name == "forwarded" || strings.HasPrefix(name, "x-forwarded-")
}

// cancelledBy calls abort once when ctx ends, at once when it has.
func cancelledBy(ctx context.Context, abort func(reason error)) {
	if ctx.Err() != nil {
		abort(context.Cause(ctx))
		return
	}
	context.AfterFunc(ctx, func() { abort(context.Cause(ctx)) })
}
