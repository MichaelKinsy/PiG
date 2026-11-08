package runtime

import (
	"crypto/tls"
	"net/http"
)

// Ports apps/p2/src/runtime/fetch.ts: the process-wide fetch of the server and session workers over HTTP/1.1. Upstream
// installs npm undici's fetch with `allowH2: false` because Node 26's bundled fetch negotiates HTTP/2 with one shared
// session per origin that can outlive its connection. Go's form of the same guarantee: ConfigureFetch makes
// http.DefaultClient speak HTTP/1.1 only (no `h2` in ALPN, so a failed connection is dropped and the next request
// opens another) and decode gzip responses (the Transport asks for gzip and decodes it).

// FetchTransport is an HTTP/1.1-only transport: no h2 in ALPN, gzip decoded.
func FetchTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
	transport.DisableCompression = false
	return transport
}

// ConfigureFetch installs FetchTransport as http.DefaultClient's transport. Call once per process, before any request.
func ConfigureFetch() { http.DefaultClient.Transport = FetchTransport() }
