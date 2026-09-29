package ai

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func clearDispatcherProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/http-dispatcher.test.ts:35,42,52
func TestHTTPProxySettingsUpstream(t *testing.T) {
	for _, tc := range []struct {
		name, setting, http, https, wantHTTP, wantHTTPS string
		present                                         bool
	}{
		{"applies httpProxy to HTTP_PROXY and HTTPS_PROXY", "http://127.0.0.1:7890", "", "", "http://127.0.0.1:7890", "http://127.0.0.1:7890", false},
		{"does not override existing proxy env vars", "http://settings:7890", "http://env-http:8080", "http://env-https:8080", "http://env-http:8080", "http://env-https:8080", true},
		{"ignores empty values", "   ", "", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearDispatcherProxyEnv(t)
			if tc.present {
				t.Setenv("HTTP_PROXY", tc.http)
				t.Setenv("HTTPS_PROXY", tc.https)
			}
			if err := ApplyHTTPProxySettings(tc.setting); err != nil {
				t.Fatal(err)
			}
			if os.Getenv("HTTP_PROXY") != tc.wantHTTP || os.Getenv("HTTPS_PROXY") != tc.wantHTTPS {
				t.Fatalf("HTTP=%q HTTPS=%q", os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"))
			}
			if tc.name == "ignores empty values" {
				for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
					if _, present := os.LookupEnv(key); present {
						t.Fatalf("%s must remain absent", key)
					}
				}
			}
		})
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/http-dispatcher.test.ts:150
func TestHTTPDispatcherAllowsTwoSecondsWithoutChangingDefault(t *testing.T) {
	clearDispatcherProxyEnv(t)
	before := reflect.ValueOf(http.DefaultTransport.(*http.Transport).DialContext).Pointer()
	old := ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() {
		if err := ConfigureHTTPDispatcher(old); err != nil {
			t.Error(err)
		}
	})
	if err := ConfigureHTTPDispatcher(DefaultHTTPIdleTimeoutMs); err != nil {
		t.Fatal(err)
	}
	if dialer := streamingDialer(net.Dialer{}); dialer.FallbackDelay != 2*time.Second {
		t.Fatalf("dialer=%+v", dialer)
	}
	if reflect.ValueOf(http.DefaultTransport.(*http.Transport).DialContext).Pointer() != before {
		t.Fatal("global default dialer changed")
	}
}

func TestHTTPProxyCancellationClosesConnectSocket(t *testing.T) {
	clearDispatcherProxyEnv(t)
	entered := make(chan struct{})
	closed := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(); close(closed) }()
		close(entered)
		var b [1]byte
		_, _ = conn.Read(b[:])
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	client := baseStreamingHTTPClient(false)
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.invalid", nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	<-closed
}

func TestHTTPProxySettingsPreserveExplicitEmptyEnvironment(t *testing.T) {
	clearDispatcherProxyEnv(t)
	t.Setenv("HTTP_PROXY", "")
	if err := ApplyHTTPProxySettings("http://settings:7890"); err != nil {
		t.Fatal(err)
	}
	if value, exists := os.LookupEnv("HTTP_PROXY"); !exists || value != "" {
		t.Fatalf("HTTP_PROXY=(%q,%v)", value, exists)
	}
	if got := os.Getenv("HTTPS_PROXY"); got != "http://settings:7890" {
		t.Fatalf("HTTPS_PROXY=%q", got)
	}
}

// .upstream/v0.87.1/packages/coding-agent/test/http-dispatcher.test.ts:93
func TestHTTPDispatcherTunnelsProxiedHTTPOrigins(t *testing.T) {
	clearDispatcherProxyEnv(t)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "origin") }))
	defer origin.Close()
	var mu sync.Mutex
	var requestLines []string
	var pumps sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestLines = append(requestLines, r.Method+" "+r.RequestURI+" "+r.Proto)
		mu.Unlock()
		if r.Method != http.MethodConnect {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		upstream, err := net.Dial("tcp", strings.TrimPrefix(origin.URL, "http://"))
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			_ = upstream.Close()
			t.Error(err)
			return
		}
		mu.Lock()
		pumps.Add(2)
		mu.Unlock()
		if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			pumps.Done()
			pumps.Done()
			_ = client.Close()
			_ = upstream.Close()
			t.Error(err)
			return
		}
		go func() {
			defer pumps.Done()
			defer func() { _ = client.Close(); _ = upstream.Close() }()
			_, _ = io.Copy(client, upstream)
		}()
		go func() {
			defer pumps.Done()
			defer func() { _ = client.Close(); _ = upstream.Close() }()
			_, _ = io.Copy(upstream, client)
		}()
	}))
	defer func() { proxy.Close(); pumps.Wait() }()
	t.Setenv("HTTP_PROXY", proxy.URL)
	client := baseStreamingHTTPClient(false)
	defer client.CloseIdleConnections()
	for range 2 {
		response, err := client.Get(origin.URL + "/v1/chat/completions")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != "origin" {
			t.Fatalf("body=%q err=%v", body, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	want := "CONNECT " + strings.TrimPrefix(origin.URL, "http://") + " HTTP/1.1"
	if len(requestLines) == 0 {
		t.Fatal("HTTP origin bypassed the proxy")
	}
	found := false
	for _, line := range requestLines {
		if line == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("requests=%v, want %q", requestLines, want)
	}
}
