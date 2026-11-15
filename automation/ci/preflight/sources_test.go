// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A POST creates something. When the answer is a 5xx or the connection breaks, the server may already have created it,
// so the request is sent once. A 429 says the request was not taken, and a GET or a PATCH repeats without harm.
func TestOnlyRepeatableRequestsAreRetried(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		fail   func(w http.ResponseWriter)
		sent   int32
		ok     bool
	}{
		{"a POST the server applied before a 502", http.MethodPost, func(w http.ResponseWriter) { http.Error(w, "bad gateway", http.StatusBadGateway) }, 1, false},
		{"a POST the server applied before the connection broke", http.MethodPost, func(w http.ResponseWriter) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		}, 1, false},
		{"a POST the server refused with 429", http.MethodPost, func(w http.ResponseWriter) { http.Error(w, "slow down", http.StatusTooManyRequests) }, 2, true},
		{"a PATCH after a 502", http.MethodPatch, func(w http.ResponseWriter) { http.Error(w, "bad gateway", http.StatusBadGateway) }, 2, true},
		{"a GET after a 502", http.MethodGet, func(w http.ResponseWriter) { http.Error(w, "bad gateway", http.StatusBadGateway) }, 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var sent atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.method {
					t.Errorf("method %s, want %s", r.Method, tc.method)
				}
				if sent.Add(1) == 1 {
					tc.fail(w)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			a := newAPI(server.URL, "t")
			a.retryDelays = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}
			var err error
			if tc.method == http.MethodGet {
				var out map[string]any
				err = a.get(context.Background(), "x", &out, false)
			} else {
				err = a.send(context.Background(), tc.method, "x", map[string]string{"body": "b"}, nil)
			}
			if got := sent.Load(); got != tc.sent || (err == nil) != tc.ok {
				t.Fatalf("%d request(s) sent, err %v; want %d request(s), success %v", got, err, tc.sent, tc.ok)
			}
		})
	}
}
