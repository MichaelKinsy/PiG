package httprequestreuse

import (
	"bytes"
	"net/http"
)

type retry struct{ base http.RoundTripper }

// RoundTrip is the shape of ai/provider_retry.go before the fix: it rewrote req.Body on the caller's request.
func (t *retry) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		req.Body = http.NoBody // want `RoundTrip assigns req.Body on the caller's request`
		return t.base.RoundTrip(req)
	}
	return resp, nil
}

type fixed struct{ base http.RoundTripper }

func (t *fixed) RoundTrip(req *http.Request) (*http.Response, error) {
	attempt := req.Clone(req.Context())
	attempt.Body = http.NoBody
	return t.base.RoundTrip(attempt)
}

func loopBad(c *http.Client, req *http.Request) {
	for range 3 {
		resp, err := c.Do(req) // want `request req is re-sent from a loop`
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

func loopGood(c *http.Client, url string, payload []byte) {
	for range 3 {
		req, _ := http.NewRequest("POST", url, bytes.NewReader(payload))
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}

func loopRewind(c *http.Client, req *http.Request) {
	for range 3 {
		req = req.Clone(req.Context())
		resp, err := c.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}
