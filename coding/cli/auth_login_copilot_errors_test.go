package cli

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

type loopbackTransport func(*http.Request) (*http.Response, error)

func (f loopbackTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// loopbackDefaultClient sends every request of http.DefaultClient to handler on a local listener, keeping the original host as the Host header.
func loopbackDefaultClient(t *testing.T, handler http.Handler) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	old := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: loopbackTransport(func(r *http.Request) (*http.Response, error) {
		out := r.Clone(r.Context())
		out.Host = r.URL.Host
		out.URL.Scheme = "http"
		out.URL.Host = server.Listener.Addr().String()
		return http.DefaultTransport.RoundTrip(out)
	})}
	t.Cleanup(func() { http.DefaultClient = old })
}

// loginGitHubCopilot (github-copilot.ts:441, 445) awaits startDeviceFlow without a catch, which rejects with `Invalid device code response` for a body that is no
// object; cli.ts (:124-127) prints `Error: <message>` for it. `pig login github-copilot` prints that message and none of PiG's own "github-copilot: device flow:".
func TestLoginCommandGitHubCopilotPrintsTheDeviceFlowFailureWithoutAPigPrefix(t *testing.T) {
	loopbackDefaultClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/login/device/code" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`"no object"`))
	}))
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	stdout, stderr, code := captureWithStdin(t, "\n", func() int { return runLoginCommand([]string{"login", "github-copilot"}) })
	if code != 1 || stderr != "Error: Invalid device code response\n" {
		t.Fatalf("code %d, stderr %q, want exactly %q\nstdout:\n%s", code, stderr, "Error: Invalid device code response\n", stdout)
	}
}
