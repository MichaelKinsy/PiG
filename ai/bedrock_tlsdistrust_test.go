//go:build !pig_strip_bedrock_converse_stream

package ai

import (
	"errors"
	"net/http"
	"net/url"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/tlsdistrust"
	"github.com/MichaelKinsy/PiG/internal/tlsdistrust/tlsdistrusttest"
)

// The Bedrock runtime client and the credential client (the STS, SSO, SSO OIDC and sign-in calls) are both built by newBedrockHTTPClient.
func TestBedrockHTTPClientsCheckTheChain(t *testing.T) {
	proxied := func(*http.Request) (*url.URL, error) { return nil, nil }
	for name, build := range map[string]func() *http.Transport{
		"credential client": func() *http.Transport { return newBedrockHTTPClient(nil).GetTransport() },
		"runtime client":    func() *http.Transport { return newBedrockHTTPClient(proxied).GetTransport() },
	} {
		transport := build()
		if transport.TLSClientConfig == nil || transport.TLSClientConfig.VerifyConnection == nil {
			t.Errorf("%s: no VerifyConnection hook", name)
			continue
		}
		err := transport.TLSClientConfig.VerifyConnection(tlsdistrusttest.BlockedState("bedrock-runtime.us-east-1.amazonaws.com", tlsdistrusttest.EmSignRootG1(t), compromisedTLSIntermediate(t)))
		if !errors.Is(err, tlsdistrust.ErrDistrusted) {
			t.Errorf("%s: VerifyConnection on a chain through a compromised CA = %v, want ErrDistrusted", name, err)
		}
	}
}
