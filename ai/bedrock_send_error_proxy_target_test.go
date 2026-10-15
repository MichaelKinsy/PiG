//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

// Pi picks the Bedrock request handler from resolveHttpProxyUrlForTarget(model.baseUrl, env) (bedrock-converse-stream.ts:220-232).
// A model with no baseUrl has no proxy target (node-http-proxy.ts:118-122 returns "" for an unparsable URL), so Pi keeps the default
// NodeHttp2Handler even when HTTPS_PROXY is set, and its refused connect is the retried "pending stream has been canceled" form.
// Only a real proxy target, or AWS_BEDROCK_FORCE_HTTP1=1, gives NodeHttpHandler's bare net error.
func TestBedrockSendErrorProxyFormFollowsTheModelBaseURL(t *testing.T) {
	refused := &url.Error{Op: "Post", URL: "https://bedrock.invalid/model/x/converse-stream", Err: &net.OpError{
		Op: "dial", Net: "tcp", Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9},
		Err: &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
	}}
	proxy := ProviderEnv{"HTTPS_PROXY": "http://127.0.0.1:8"}
	for _, tc := range []struct {
		name    string
		baseURL string
		env     ProviderEnv
		want    string
	}{
		{"no baseUrl, proxy set", "", proxy, "The pending stream has been canceled (caused by: connect ECONNREFUSED 127.0.0.1:9)"},
		{"baseUrl through the proxy", "https://bedrock-runtime.us-east-1.amazonaws.com", proxy, "connect ECONNREFUSED 127.0.0.1:9"},
		{"baseUrl, no proxy", "https://bedrock-runtime.us-east-1.amazonaws.com", ProviderEnv{}, "The pending stream has been canceled (caused by: connect ECONNREFUSED 127.0.0.1:9)"},
		{"no baseUrl, forced HTTP/1", "", ProviderEnv{"AWS_BEDROCK_FORCE_HTTP1": "1"}, "connect ECONNREFUSED 127.0.0.1:9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bedrockSendError(t.Context(), refused, tc.baseURL, tc.env).Error(); got != tc.want {
				t.Fatalf("bedrockSendError = %q, want Pi's %q", got, tc.want)
			}
		})
	}
}
