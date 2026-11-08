package mcpext

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/mcp"
)

// recordingTransport records the time left before the deadline of every request, then fails it.
// oauthRequestTimeout is OAUTH_REQUEST_TIMEOUT_MS of packages/mcp/src/oauth/client-settings.ts.
const oauthRequestTimeout = 15 * time.Second

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

// The sign-in flow bounds its authorization-server requests the same way.
func TestSignInMcpServerGivesEveryAuthorizationServerRequestTheOAuthRequestTimeout(t *testing.T) {
	recorder := &recordingTransport{}
	original := nodeFetch
	nodeFetch = func() mcp.McpFetch { return &http.Client{Transport: recorder} }
	t.Cleanup(func() { nodeFetch = original })
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
