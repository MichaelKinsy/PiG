package ai

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// undici's env proxy (the one Pi's WebSocket goes through) reads the lowercase variable first, then the uppercase: https_proxy before HTTPS_PROXY.
// net/http.ProxyFromEnvironment reads the uppercase one first, so the Codex WebSocket dialed the other proxy when both variables are set.
func TestCodexWebSocketDialsTheLowercaseHTTPSProxyFirst(t *testing.T) {
	listen := func() (net.Listener, chan string) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		lines := make(chan string, 1)
		go func() {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			defer func() { _ = connection.Close() }()
			line, _ := bufio.NewReader(connection).ReadString('\n')
			lines <- strings.TrimSpace(line)
		}()
		return listener, lines
	}
	upper, upperLines := listen()
	lower, lowerLines := listen()
	t.Setenv("HTTPS_PROXY", "http://"+upper.Addr().String())
	t.Setenv("https_proxy", "http://"+lower.Addr().String())
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _ = acquireCodexWebSocket(ctx, "https://chatgpt.example.test/backend-api", nil, "", "", 3*time.Second)

	select {
	case line := <-lowerLines:
		if !strings.HasPrefix(line, "CONNECT chatgpt.example.test:443") {
			t.Fatalf("lowercase proxy got %q, want a CONNECT to the target", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the lowercase https_proxy was not dialed")
	}
	select {
	case line := <-upperLines:
		t.Fatalf("the uppercase HTTPS_PROXY was dialed too: %q", line)
	default:
	}
}

// EnvHttpProxyAgent (undici 8.10.2 env-http-proxy-agent.js:26-37) sends an https request through the http proxy when https_proxy ?? HTTPS_PROXY is empty,
// and an https_proxy that is set but empty counts as empty.
func TestCodexWebSocketFallsBackToTheHTTPProxy(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	lines := make(chan string, 1)
	go func() {
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		line, _ := bufio.NewReader(connection).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	// Clear the others first: Windows environment names are case-insensitive, so clearing HTTP_PROXY afterwards would clear http_proxy too.
	for _, name := range []string{"HTTP_PROXY", "https_proxy", "HTTPS_PROXY", "NO_PROXY", "no_proxy"} {
		t.Setenv(name, "")
	}
	t.Setenv("http_proxy", "http://"+listener.Addr().String())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _ = acquireCodexWebSocket(ctx, "https://chatgpt.example.test/backend-api", nil, "", "", 3*time.Second)

	select {
	case line := <-lines:
		if !strings.HasPrefix(line, "CONNECT chatgpt.example.test:443") {
			t.Fatalf("http proxy got %q, want a CONNECT to the target", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the https request did not fall back to http_proxy")
	}
}

// EnvHttpProxyAgent dials a host NO_PROXY names directly (env-http-proxy-agent.js:89-118), so the Codex WebSocket must not send it to the proxy.
func TestCodexWebSocketDialsANoProxyHostDirectly(t *testing.T) {
	accept := func() (net.Listener, chan struct{}) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = listener.Close() })
		accepted := make(chan struct{}, 1)
		go func() {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			_ = connection.Close()
		}()
		return listener, accepted
	}
	proxy, proxyAccepted := accept()
	target, targetAccepted := accept()
	t.Setenv("https_proxy", "http://"+proxy.Addr().String())
	t.Setenv("no_proxy", "127.0.0.1")
	for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "NO_PROXY"} {
		t.Setenv(name, "")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	_, _ = acquireCodexWebSocket(ctx, "https://"+target.Addr().String()+"/backend-api", nil, "", "", 3*time.Second)

	select {
	case <-targetAccepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the NO_PROXY host was not dialed directly")
	}
	select {
	case <-proxyAccepted:
		t.Fatal("the proxy was dialed for a NO_PROXY host")
	default:
	}
}

// The Codex WebSocket proxy follows EnvHttpProxyAgent as PiG's HTTP transport does: the lowercase variable wins, an https target falls back to the
// http proxy, NO_PROXY bypasses a named host and its subdomains, and a set but empty lowercase variable hides the uppercase one.
func TestCodexWebSocketProxyFollowsEnvHttpProxyAgent(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		target string
		want   string
		// caseOnly marks a case that sets two names differing only in case, which Windows (and Node there) cannot hold apart.
		caseOnly bool
	}{
		{"lowercase https first", map[string]string{"https_proxy": "http://lower:1", "HTTPS_PROXY": "http://upper:1"}, "https://chatgpt.com/x", "http://lower:1", true},
		{"https falls back to http", map[string]string{"HTTP_PROXY": "http://plain:1"}, "https://chatgpt.com/x", "http://plain:1", false},
		{"http target ignores https proxy", map[string]string{"HTTPS_PROXY": "http://secure:1"}, "http://chatgpt.com/x", "", false},
		{"NO_PROXY names the host", map[string]string{"HTTPS_PROXY": "http://secure:1", "NO_PROXY": "chatgpt.com"}, "https://chatgpt.com/x", "", false},
		{"NO_PROXY names a parent domain", map[string]string{"HTTPS_PROXY": "http://secure:1", "no_proxy": ".example.test"}, "https://api.example.test/x", "", false},
		{"empty lowercase hides uppercase", map[string]string{"https_proxy": "", "HTTPS_PROXY": "http://secure:1"}, "https://chatgpt.com/x", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.caseOnly && runtime.GOOS == "windows" {
				t.Skip("Windows environment names are case-insensitive")
			}
			for _, name := range []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "NO_PROXY", "no_proxy"} {
				t.Setenv(name, "")
				_ = os.Unsetenv(name)
			}
			for name, value := range tc.env {
				t.Setenv(name, value)
			}
			request, err := http.NewRequest(http.MethodGet, tc.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			proxy, err := proxyFromEnvironment(request)
			if err != nil {
				t.Fatal(err)
			}
			got := ""
			if proxy != nil {
				got = proxy.String()
			}
			if got != tc.want {
				t.Fatalf("proxy = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("invalid proxy URL", func(t *testing.T) {
		t.Setenv("https_proxy", "socks5://proxy:1080")
		request, _ := http.NewRequest(http.MethodGet, "https://chatgpt.com/x", nil)
		if _, err := proxyFromEnvironment(request); err == nil {
			t.Fatal("a SOCKS proxy URL was accepted")
		}
	})
}
