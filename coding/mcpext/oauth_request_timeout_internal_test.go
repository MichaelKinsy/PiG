package mcpext

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// recordingTransport records the time left before the deadline of every request, then fails it.
type recordingTransport struct{ remaining []time.Duration }

var errStopped = errors.New("stopped")

func (r *recordingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	deadline, ok := request.Context().Deadline()
	if !ok {
		r.remaining = append(r.remaining, -1)
	} else {
		r.remaining = append(r.remaining, time.Until(deadline))
	}
	return nil, errStopped
}

func requireRequestTimeout(t *testing.T, remaining []time.Duration) {
	t.Helper()
	if len(remaining) == 0 {
		t.Fatal("no request was sent")
	}
	for _, left := range remaining {
		if left <= oauthRequestTimeout-time.Second || left > oauthRequestTimeout {
			t.Fatalf("request deadline in %s, want within (%s, %s]", left, oauthRequestTimeout-time.Second, oauthRequestTimeout)
		}
	}
}

// oauth.ts OAUTH_REQUEST_TIMEOUT_MS / timedFetch (1.1.0, #10565): each request to the authorization server of a
// refresh times out after 15 seconds.
func TestTimedFetchGivesEveryRequestTheOAuthRequestTimeout(t *testing.T) {
	if oauthRequestTimeout != 15*time.Second {
		t.Fatalf("oauthRequestTimeout = %s, want 15s", oauthRequestTimeout)
	}
	recorder := &recordingTransport{}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1/", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := timedFetch(&http.Client{Transport: recorder}).Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, errStopped) {
		t.Fatalf("err = %v", err)
	}
	requireRequestTimeout(t, recorder.remaining)
}

// The sign-in flow bounds its authorization-server requests the same way.
func TestSignInMcpServerGivesEveryAuthorizationServerRequestTheOAuthRequestTimeout(t *testing.T) {
	recorder := &recordingTransport{}
	original := http.DefaultClient.Transport
	http.DefaultClient.Transport = recorder
	t.Cleanup(func() { http.DefaultClient.Transport = original })
	serverURL := "http://127.0.0.1:1/mcp"
	store, err := NewMcpOAuthCredentialStoreWithBackend(&InMemoryAuthStorageBackend{}, t.TempDir()).ForServer("test", serverURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := SignInMcpServer(t.Context(), SignInOptions{ServerURL: serverURL, Store: store}); err == nil {
		t.Fatal("the sign-in succeeded")
	}
	requireRequestTimeout(t, recorder.remaining)
}
