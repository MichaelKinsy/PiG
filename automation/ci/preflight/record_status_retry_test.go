// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const retryTestToken = "tok-SECRET-123"

// statusServer answers the status POST with fail for the first failures requests and then takes it; it answers a
// comment POST with comment. It keeps what every request held.
type statusServer struct {
	mu       sync.Mutex
	failures int
	fail     func(w http.ResponseWriter)
	comment  func(w http.ResponseWriter)
	statuses []map[string]string
	auth     []string
	comments int
	seen     func()
}

func (s *statusServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !strings.Contains(r.URL.Path, "/statuses/") {
		s.comments++
		if s.comment != nil {
			s.comment(w)
			return
		}
		_, _ = io.WriteString(w, `{"id":1}`)
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	s.statuses = append(s.statuses, body)
	s.auth = append(s.auth, r.Header.Get("Authorization"))
	if s.seen != nil {
		s.seen()
	}
	if len(s.statuses) <= s.failures && s.fail != nil {
		s.fail(w)
		return
	}
	w.WriteHeader(http.StatusCreated)
	_, _ = io.WriteString(w, `{}`)
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "request timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// lostAnswers lets the server take the first n requests and loses their answers, as a timeout does.
type lostAnswers struct {
	mu sync.Mutex
	n  int
}

func (l *lostAnswers) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n > 0 {
		l.n--
		_ = resp.Body.Close()
		return nil, timeoutError{}
	}
	return resp, nil
}

// statusRun records testEvaluation's verdict through an API whose waits are kept, not slept.
func statusRun(ctx context.Context, t *testing.T, g *statusServer, prepare func(a *api)) (waits []time.Duration, log string, err error) {
	t.Helper()
	server := httptest.NewServer(g)
	defer server.Close()
	cfg, d, ev := testEvaluation()
	a := newAPI(server.URL, retryTestToken)
	a.wait = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}
	if prepare != nil {
		prepare(a)
	}
	var out strings.Builder
	a.log = &out
	err = newRecording(cfg, d, ev, prComment{}).sendVia(ctx, a, &out)
	if err != nil && strings.Contains(err.Error(), retryTestToken) || strings.Contains(out.String(), retryTestToken) {
		t.Errorf("the token is in the error or the log: err %v, log:\n%s", err, out.String())
	}
	return waits, out.String(), err
}

func refuse(status int, header ...string) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		for i := 0; i+1 < len(header); i += 2 {
			w.Header().Set(header[i], header[i+1])
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"message":"refused with %d"}`, status)
	}
}

func breakConnection(w http.ResponseWriter) {
	if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
		_ = conn.Close()
	}
}

// A commit status is one per head and context: the same state sent again changes nothing. So a status POST that met a
// passing fault is sent again, the same each time, until the server takes it.
func TestStatusPostIsRepeatedUntilTheServerTakesIt(t *testing.T) {
	failures := len(defaultRetryDelays)
	for _, tc := range []struct {
		name    string
		fail    func(w http.ResponseWriter)
		prepare func(a *api)
	}{
		{"502", refuse(http.StatusBadGateway), nil},
		{"503", refuse(http.StatusServiceUnavailable), nil},
		{"a broken connection", breakConnection, nil},
		{"429 with Retry-After", refuse(http.StatusTooManyRequests, "Retry-After", "1"), nil},
		{"a timeout after the server took it", nil, func(a *api) { a.client.Transport = &lostAnswers{n: failures} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &statusServer{failures: failures, fail: tc.fail}
			waits, log, err := statusRun(context.Background(), t, g, tc.prepare)
			if err != nil {
				t.Fatalf("the status was not recorded after %d request(s): %v", len(g.statuses), err)
			}
			if len(g.statuses) != failures+1 {
				t.Fatalf("%d status request(s), want %d", len(g.statuses), failures+1)
			}
			want := map[string]string{"state": "failure", "description": "1 failed: template", "context": statusContext}
			for i, got := range g.statuses {
				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("request %d held %v, want %v", i+1, got, want)
				}
				if g.auth[i] != "Bearer "+retryTestToken {
					t.Errorf("request %d was not authorized", i+1)
				}
			}
			if !slices.Equal(waits, defaultRetryDelays) {
				t.Errorf("waited %v, want %v", waits, defaultRetryDelays)
			}
			if !strings.Contains(log, "record: status failure on") || g.comments != 1 {
				t.Errorf("%d comment request(s), log:\n%s", g.comments, log)
			}
		})
	}
}

func TestStatusPostStopsAfterItsAttempts(t *testing.T) {
	g := &statusServer{failures: 1 << 20, fail: refuse(http.StatusBadGateway)}
	waits, _, err := statusRun(context.Background(), t, g, nil)
	attempts := len(defaultRetryDelays) + 1
	if err == nil || len(g.statuses) != attempts || !slices.Equal(waits, defaultRetryDelays) {
		t.Fatalf("%d request(s), waited %v, err %v; want %d request(s) and an error", len(g.statuses), waits, err, attempts)
	}
	for _, want := range []string{"POST repos/o/r/statuses/head1234567", "HTTP 502", "refused with 502"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error %q does not name %q", err, want)
		}
	}
	if !hasStatus(err, http.StatusBadGateway) || g.comments != 0 {
		t.Errorf("err %v, %d comment request(s)", err, g.comments)
	}
}

// The bound is stated against the files that set the time there is: the job's timeout less the run's time budget in
// preflight.yml, and the time wait-preflight.sh waits for the verdict.
func TestStatusRetryFitsTheJobAndTheWait(t *testing.T) {
	// TestMain holds the delays to 2, 4 and 6 s before it shortens them for the tests.
	attempts := len(defaultRetryDelays) + 1
	worst := time.Duration(attempts)*apiTimeout + (2+4+6)*time.Second
	if attempts != 4 || worst != 132*time.Second {
		t.Fatalf("%d attempts, %s at worst; setStatus's comment says 4 and 132 s", attempts, worst)
	}
	root := filepath.Join("..", "..", "..")
	workflow, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "preflight.yml"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := regexp.MustCompile(`(?s)timeout-minutes: (\d+)\n.*?--time-budget (\S+)`).FindAllStringSubmatch(string(workflow), -1)
	if len(jobs) != 2 {
		t.Fatalf("%d job(s) with a timeout and a time budget in preflight.yml, want 2", len(jobs))
	}
	for _, j := range jobs {
		minutes, _ := strconv.Atoi(j[1])
		budget, err := time.ParseDuration(j[2])
		if err != nil {
			t.Fatal(err)
		}
		if left := time.Duration(minutes)*time.Minute - budget; worst > left {
			t.Errorf("the status takes %s at worst; the job leaves %s after its time budget", worst, left)
		}
	}
	script, err := os.ReadFile(filepath.Join(root, "automation", "ci", "wait-preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`PREFLIGHT_WAIT_SECONDS:-(\d+)`).FindSubmatch(script)
	if m == nil {
		t.Fatal("no default of PREFLIGHT_WAIT_SECONDS in wait-preflight.sh")
	}
	if seconds, _ := strconv.Atoi(string(m[1])); worst > time.Duration(seconds)*time.Second {
		t.Errorf("the status takes %s at worst; wait-preflight.sh waits %d s", worst, seconds)
	}
}

func TestStatusPostIsNotRepeatedAfterAnAnswer(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			g := &statusServer{failures: 1 << 20, fail: refuse(status, "Retry-After", "1")}
			waits, _, err := statusRun(context.Background(), t, g, nil)
			if err == nil || len(g.statuses) != 1 || len(waits) != 0 || g.comments != 0 {
				t.Fatalf("%d status request(s), waited %v, %d comment request(s), err %v; want one request and an error", len(g.statuses), waits, g.comments, err)
			}
			for _, want := range []string{"POST repos/o/r/statuses/head1234567", fmt.Sprintf("HTTP %d", status), fmt.Sprintf("refused with %d", status)} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the error %q does not name %q", err, want)
				}
			}
			if !hasStatus(err, status) {
				t.Errorf("status %d is not read back: %v", status, err)
			}
		})
	}
}

// A comment POST the server may have taken would be a second comment when sent again.
func TestCommentPostIsSentOnce(t *testing.T) {
	for _, tc := range []struct {
		name    string
		comment func(w http.ResponseWriter)
	}{
		{"502", refuse(http.StatusBadGateway)},
		{"a broken connection", breakConnection},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &statusServer{comment: tc.comment}
			waits, _, err := statusRun(context.Background(), t, g, nil)
			if err == nil || g.comments != 1 || len(g.statuses) != 1 || len(waits) != 0 {
				t.Fatalf("%d comment request(s), %d status request(s), waited %v, err %v; want one of each and an error", g.comments, len(g.statuses), waits, err)
			}
		})
	}
}

func TestStatusRetryStopsWhenTheRunIsCancelled(t *testing.T) {
	t.Run("in the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := &statusServer{failures: 1 << 20, fail: refuse(http.StatusBadGateway)}
		// The real wait, an hour long: only the cancellation ends it.
		_, _, err := statusRun(ctx, t, g, func(a *api) {
			a.retryDelays = []time.Duration{time.Hour}
			a.wait = func(ctx context.Context, d time.Duration) error {
				cancel()
				return sleep(ctx, d)
			}
		})
		if !errors.Is(err, context.Canceled) || len(g.statuses) != 1 {
			t.Fatalf("%d request(s), err %v; want one request and the cancellation", len(g.statuses), err)
		}
	})
	t.Run("in the request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		g := &statusServer{failures: 1 << 20, fail: refuse(http.StatusBadGateway), seen: cancel}
		waits, _, err := statusRun(ctx, t, g, nil)
		if err == nil || len(g.statuses) != 1 || len(waits) != 0 {
			t.Fatalf("%d request(s), waited %v, err %v; want one request, no wait and an error", len(g.statuses), waits, err)
		}
	})
}
