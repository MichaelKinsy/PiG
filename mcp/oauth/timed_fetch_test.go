package oauth

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

// client-settings.ts OAUTH_REQUEST_TIMEOUT_MS / timedFetch (Pi 1.1.0 #10565, moved by bb266bf35): each request to the authorization server of a
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
	response, err := TimedFetch(&http.Client{Transport: recorder}).Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if !errors.Is(err, errStopped) {
		t.Fatalf("err = %v", err)
	}
	if len(recorder.remaining) != 1 || recorder.remaining[0] <= oauthRequestTimeout-time.Second || recorder.remaining[0] > oauthRequestTimeout {
		t.Fatalf("request deadlines = %v", recorder.remaining)
	}
}
