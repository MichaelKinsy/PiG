package runtime

import (
	"compress/gzip"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ports "configureFetch installs an HTTP/1.1 fetch that decodes compressed responses" (apps/p2/test/p2/fetch.test.ts):
// the server offers HTTP/2 through ALPN and answers gzip; the client must stay on HTTP/1.1 and still decompress.
func TestConfigureFetchInstallsAnHTTP11FetchThatDecodesCompressedResponses(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Http-Version", r.Proto)
		w.Header().Set("Content-Encoding", "gzip")
		zipped := gzip.NewWriter(w)
		_, _ = zipped.Write([]byte("hello " + r.URL.Path))
		_ = zipped.Close()
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()

	previous := http.DefaultClient.Transport
	defer func() { http.DefaultClient.Transport = previous }()
	ConfigureFetch()
	transport := http.DefaultClient.Transport.(*http.Transport)
	transport.TLSClientConfig.InsecureSkipVerify = true
	transport.TLSClientConfig.NextProtos = []string{"http/1.1"}

	for _, path := range []string{"/a", "/b"} {
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got := response.Header.Get("X-Http-Version"); got != "HTTP/1.1" {
			t.Errorf("%s: protocol %q, want HTTP/1.1", path, got)
		}
		if string(body) != "hello "+path {
			t.Errorf("%s: body %q", path, body)
		}
	}
	// The same server does speak HTTP/2 to a client that asks for it.
	h2 := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}, ForceAttemptHTTP2: true}}
	response, err := h2.Get(server.URL + "/c")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !strings.HasPrefix(response.Proto, "HTTP/2") {
		t.Errorf("control client used %s: the test server must offer h2", response.Proto)
	}
}
