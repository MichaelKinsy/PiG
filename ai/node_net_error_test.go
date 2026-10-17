package ai

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"testing"
)

// Node reports a refused connect as `connect ECONNREFUSED <address>:<port>` (net.js exceptionWithHostPort), with no
// brackets around an IPv6 address, and a failed lookup as `getaddrinfo ENOTFOUND <host>`. A proxy failure names the proxy.
func TestNodeNetErrorMessageIsNodesText(t *testing.T) {
	ipv4 := closedTCPAddress(t, "tcp4", "127.0.0.1:0")
	_, err := (&net.Dialer{}).DialContext(t.Context(), "tcp", ipv4.String())
	if got, ok := nodeNetErrorMessage(err); !ok || got != "connect ECONNREFUSED 127.0.0.1:"+strconv.Itoa(ipv4.Port) {
		t.Errorf("dial: %q %v (from %v)", got, ok, err)
	}

	proxy := &url.URL{Scheme: "http", Host: ipv4.String()}
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxy)}}
	request, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://bedrock-runtime.us-east-1.amazonaws.com/", nil)
	_, err = client.Do(request) //nolint:bodyclose // The request fails before any response.
	if got, ok := nodeNetErrorMessage(err); !ok || got != "connect ECONNREFUSED 127.0.0.1:"+strconv.Itoa(ipv4.Port) {
		t.Errorf("proxy: %q %v (from %v)", got, ok, err)
	}

	if got, ok := nodeNetErrorMessage(&url.Error{Op: "Post", URL: "https://x.invalid", Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "x.invalid", IsNotFound: true}}}); !ok || got != "getaddrinfo ENOTFOUND x.invalid" {
		t.Errorf("dns: %q %v", got, ok)
	}
	if _, ok := nodeNetErrorMessage(context.Canceled); ok {
		t.Error("a non-network error has Node text")
	}

	// closedTCPAddress skips where the host has no IPv6 loopback, so this case runs last.
	if ipv6 := closedTCPAddress(t, "tcp6", "[::1]:0"); ipv6 != nil {
		_, err = (&net.Dialer{}).DialContext(t.Context(), "tcp", ipv6.String())
		if got, ok := nodeNetErrorMessage(err); !ok || got != "connect ECONNREFUSED ::1:"+strconv.Itoa(ipv6.Port) {
			t.Errorf("ipv6: %q %v (from %v)", got, ok, err)
		}
	}

}
