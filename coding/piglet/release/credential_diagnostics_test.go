package release

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

type credentialProbeTransport struct {
	err    error
	called bool
}

func (p *credentialProbeTransport) RoundTrip(*http.Request) (*http.Response, error) {
	p.called = true
	return nil, p.err
}

func TestDistributionCredentialDiagnostics(t *testing.T) {
	const secret = "distribution-secret-must-not-appear"
	for _, tc := range []struct {
		name, ref string
		request   bool
	}{
		{"password", "https://user:" + secret + "@releases.example/index", false},
		{"username token", "https://" + secret + "@releases.example/index", false},
		{"rejected HTTP query", "http://releases.example/index?token=" + secret, false},
		{"transport failure with query", "https://releases.example/index?token=" + secret, true},
		{"malformed GitHub reference", "github:acme:" + secret + "/repo@1.0.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PIG_HOME", t.TempDir())
			cause := errors.New("transport refused " + tc.ref)
			transport := &credentialProbeTransport{err: cause}
			_, err := Pull(t.Context(), tc.ref, Options{Client: &http.Client{Transport: transport}})
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Errorf("credential diagnostic: %v", err)
			}
			if transport.called != tc.request {
				t.Errorf("transport called=%t, want %t", transport.called, tc.request)
			}
			if tc.request && !errors.Is(err, cause) {
				t.Errorf("transport error identity lost: %v", err)
			}
		})
	}
}

func TestIndexURLDiagnosticsDoNotEchoCredentials(t *testing.T) {
	const secret = "index-url-secret"
	for _, raw := range []string{"https://user:" + secret + "@bad%host/binary", "https://releases.example/binary#" + secret} {
		_, err := Sign(Index{Piglet: "porter", Version: "1.0.0", PigVersion: "pig-test", SourceRef: "npm:porter@1.0.0", Binaries: map[string]Binary{testTarget: {URL: raw, SHA256: strings.Repeat("a", 64), Size: 1}}}, newKey(t))
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("index URL diagnostic: %v", err)
		}
	}
	if _, err := resolveBinaryURL("https://releases.example/index", "https://user:"+secret+"@bad%host/binary"); err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("binary URL diagnostic: %v", err)
	}
}

func TestPrivateReleaseRequestErrorsPreserveCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		client := &http.Client{Transport: &credentialProbeTransport{err: cause}}
		response, err := doRequest(t.Context(), client, "https://releases.example/index?token=private-query-token")
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), cause.Error()) || strings.Contains(err.Error(), "private-query-token") {
			t.Fatalf("cancellation diagnostic: %v", err)
		}
	}
}
