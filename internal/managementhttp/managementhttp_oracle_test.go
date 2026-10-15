package managementhttp

import (
	"context"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pioracle"
)

type fetchProbe struct {
	Statuses       []int `json:"statuses"`
	MaxRetries     *int  `json:"maxRetries,omitempty"`
	RetryOnStatus  *bool `json:"retryOnStatus,omitempty"`
	TimeoutMs      int   `json:"timeoutMs,omitempty"`
	AttemptTimeout int   `json:"attemptTimeoutMs,omitempty"`
}

type fetchOutcome struct {
	Status   int  `json:"status"`
	Failed   bool `json:"failed"`
	Requests int  `json:"requests"`
}

// fetchWithRetry against Pi's (utils/management-http.js) over a local server. Each probe scripts one status per attempt (0 hangs for 250 ms, -1 drops the
// connection, the last entry repeats): which statuses retry, maxRetries and retryOnStatus, the overall timeout (terminal) and the attempt timeout (retried).
// The compared outcome is the final status or failure and how many requests the server saw.
func TestFetchWithRetryMatchesPi(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	statuses := []int{200, 200, 204, 301, 404, 408, 425, 429, 500, 501, 502, 503, 504, 0, -1}
	ints := func(vals ...int) *int { v := vals[rng.Intn(len(vals))]; return &v }
	var probes []fetchProbe
	for range 150 {
		p := fetchProbe{}
		for range 1 + rng.Intn(4) {
			p.Statuses = append(p.Statuses, statuses[rng.Intn(len(statuses))])
		}
		if rng.Intn(3) > 0 {
			p.MaxRetries = ints(0, 1, 2, 3, -1, -5)
		}
		if rng.Intn(3) == 0 {
			v := rng.Intn(2) == 0
			p.RetryOnStatus = &v
		}
		if rng.Intn(4) == 0 {
			p.TimeoutMs = []int{60, 120, 600}[rng.Intn(3)]
		}
		if rng.Intn(4) == 0 {
			p.AttemptTimeout = []int{40, 100, 600}[rng.Intn(3)]
		}
		probes = append(probes, p)
	}
	// A hung attempt is retried only with an attempt timeout, and the overall timeout stays terminal.
	one, two := 1, 2
	probes = append(probes,
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: 40},
		fetchProbe{Statuses: []int{0, 0, 200}, AttemptTimeout: 40, MaxRetries: &two},
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: 40, MaxRetries: &one, TimeoutMs: 600},
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: 40, TimeoutMs: 30},
		fetchProbe{Statuses: []int{0, 200}, TimeoutMs: 60},
		fetchProbe{Statuses: []int{0, 200}},
	)
	var mu sync.Mutex
	counts := map[int]int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/p"))
		mu.Lock()
		attempt := counts[n]
		counts[n]++
		mu.Unlock()
		script := probes[n].Statuses
		status := script[min(attempt, len(script)-1)]
		switch status {
		case 0:
			select {
			case <-time.After(250 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			w.WriteHeader(200)
		case -1:
			conn, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = conn.Close()
			}
		default:
			if status == 301 {
				w.Header().Set("Location", "/p"+strconv.Itoa(n))
				status = 304
			}
			w.WriteHeader(status)
		}
	}))
	defer server.Close()
	type input struct {
		URL    string       `json:"url"`
		Probes []fetchProbe `json:"probes"`
	}
	var want []fetchOutcome
	pioracle.Run(t, `
const { fetchWithRetry } = await load("pi-coding-agent/utils/management-http.js");
const out = [];
for (let i = 0; i < input.probes.length; i++) {
	const p = input.probes[i];
	const options = {};
	if (p.maxRetries !== undefined) options.maxRetries = p.maxRetries;
	if (p.retryOnStatus !== undefined) options.retryOnStatus = p.retryOnStatus;
	if (p.timeoutMs) options.timeoutMs = p.timeoutMs;
	if (p.attemptTimeoutMs) options.attemptTimeoutMs = p.attemptTimeoutMs;
	try {
		const response = await fetchWithRetry(input.url + "/p" + i, { redirect: "manual" }, options);
		await response.body?.cancel();
		out.push({ status: response.status, failed: false });
	} catch {
		out.push({ status: 0, failed: true });
	}
}
emit(out);
`, input{URL: server.URL, Probes: probes}, &want)
	mu.Lock()
	for i := range want {
		want[i].Requests = counts[i]
		counts[i] = 0
	}
	mu.Unlock()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	bad := 0
	for i, p := range probes {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/p"+strconv.Itoa(i), nil)
		if err != nil {
			t.Fatal(err)
		}
		options := FetchRetryOptions{MaxRetries: p.MaxRetries, RetryOnStatus: p.RetryOnStatus,
			Timeout: time.Duration(p.TimeoutMs) * time.Millisecond, AttemptTimeout: time.Duration(p.AttemptTimeout) * time.Millisecond}
		got := fetchOutcome{}
		response, err := FetchWithRetry(client, req, options)
		if err != nil {
			got.Failed = true
		} else {
			got.Status = response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
		mu.Lock()
		got.Requests = counts[i]
		mu.Unlock()
		if got != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("probe %d %+v (max=%v retryOnStatus=%v): got %+v, want %+v", i, p, deref(p.MaxRetries), p.RetryOnStatus != nil && *p.RetryOnStatus, got, want[i])
			}
		}
	}
	if bad > 8 {
		t.Errorf("%d of %d probes differ", bad, len(probes))
	}
}

func deref(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}
