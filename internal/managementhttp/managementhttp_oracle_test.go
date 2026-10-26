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

// probeConcurrency is how many probes each side runs at once. The stretched timeouts make a probe with a hung attempt take seconds, so running all of
// them one after another would take about a minute; this many in flight keeps that to a few seconds while a loaded machine still answers each
// request within a small fraction of the shortest timeout (a hundred and fifty requests at once did not).
const probeConcurrency = 6

// timeScale stretches every wall-clock quantity of the probes below. A probe is decided by which side of a timeout an event falls on: a hung
// attempt against the attempt timeout, an immediate answer against it. The answer takes a few milliseconds when the machine is idle and
// tens of milliseconds when its CPU quota is shared with other test processes, in Pi's process as much as in this one, so a 40 ms attempt
// timeout turned an immediate 200 into a timeout on the loaded side and the comparison reported a difference between two correct
// implementations. At this scale the shortest timeout is 320 ms against an answer that needs milliseconds, and the ratios between the
// hang, the attempt timeouts and the overall timeouts are the ones the comparison was designed with.
const timeScale = 8

// fetchWithRetry against Pi's (utils/management-http.js) over a local server. Each probe scripts one status per attempt (0 hangs for 250 ms, -1 drops the
// connection, the last entry repeats; every duration is stretched by timeScale): which statuses retry, maxRetries and retryOnStatus, the overall timeout
// (terminal) and the attempt timeout (retried). The compared outcome is the final status or failure and how many requests the server saw. The probes are
// independent (each has its own path on the server), so both sides run probeConcurrency of them at once.
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
			p.TimeoutMs = timeScale * []int{60, 120, 600}[rng.Intn(3)]
		}
		if rng.Intn(4) == 0 {
			p.AttemptTimeout = timeScale * []int{40, 100, 600}[rng.Intn(3)]
		}
		probes = append(probes, p)
	}
	// A hung attempt is retried only with an attempt timeout, and the overall timeout stays terminal.
	one, two := 1, 2
	probes = append(probes,
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: timeScale * 40},
		fetchProbe{Statuses: []int{0, 0, 200}, AttemptTimeout: timeScale * 40, MaxRetries: &two},
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: timeScale * 40, MaxRetries: &one, TimeoutMs: timeScale * 600},
		fetchProbe{Statuses: []int{0, 200}, AttemptTimeout: timeScale * 40, TimeoutMs: timeScale * 30},
		fetchProbe{Statuses: []int{0, 200}, TimeoutMs: timeScale * 60},
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
			case <-time.After(timeScale * 250 * time.Millisecond):
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
		URL         string       `json:"url"`
		Probes      []fetchProbe `json:"probes"`
		Concurrency int          `json:"concurrency"`
	}
	var want []fetchOutcome
	pioracle.Run(t, `
const { fetchWithRetry } = await load("pi-coding-agent/utils/management-http.js");
const out = new Array(input.probes.length);
let next = 0;
const probe = async (p, i) => {
	const options = {};
	if (p.maxRetries !== undefined) options.maxRetries = p.maxRetries;
	if (p.retryOnStatus !== undefined) options.retryOnStatus = p.retryOnStatus;
	if (p.timeoutMs) options.timeoutMs = p.timeoutMs;
	if (p.attemptTimeoutMs) options.attemptTimeoutMs = p.attemptTimeoutMs;
	try {
		const response = await fetchWithRetry(input.url + "/p" + i, { redirect: "manual" }, options);
		await response.body?.cancel();
		return { status: response.status, failed: false };
	} catch {
		return { status: 0, failed: true };
	}
};
await Promise.all(Array.from({ length: input.concurrency }, async () => {
	while (next < input.probes.length) {
		const i = next++;
		out[i] = await probe(input.probes[i], i);
	}
}));
emit(out);
`, input{URL: server.URL, Probes: probes, Concurrency: probeConcurrency}, &want)
	mu.Lock()
	for i := range want {
		want[i].Requests = counts[i]
		counts[i] = 0
	}
	mu.Unlock()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	got := make([]fetchOutcome, len(probes))
	var wg sync.WaitGroup
	slots := make(chan struct{}, probeConcurrency)
	for i, p := range probes {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/p"+strconv.Itoa(i), nil)
			if err != nil {
				t.Error(err)
				return
			}
			options := FetchRetryOptions{MaxRetries: p.MaxRetries, RetryOnStatus: p.RetryOnStatus,
				Timeout: time.Duration(p.TimeoutMs) * time.Millisecond, AttemptTimeout: time.Duration(p.AttemptTimeout) * time.Millisecond}
			response, err := FetchWithRetry(client, req, options)
			if err != nil {
				got[i].Failed = true
				return
			}
			got[i].Status = response.StatusCode
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}()
	}
	wg.Wait()
	bad := 0
	for i, p := range probes {
		mu.Lock()
		got[i].Requests = counts[i]
		mu.Unlock()
		if got[i] != want[i] {
			if bad++; bad <= 8 {
				t.Errorf("probe %d %+v (max=%v retryOnStatus=%v): got %+v, want %+v", i, p, deref(p.MaxRetries), p.RetryOnStatus != nil && *p.RetryOnStatus, got[i], want[i])
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
