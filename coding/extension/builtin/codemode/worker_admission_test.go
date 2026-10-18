package codemode

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// Ports packages/coding-agent/test/codemode-worker-admission.test.ts.

func runCode(ctx context.Context, t *testing.T, code string) agent.AgentToolResult {
	t.Helper()
	params, _ := json.Marshal(map[string]string{"code": code})
	result, err := Execute(ctx, "worker-admission", params, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func resultText(result agent.AgentToolResult) string {
	var b strings.Builder
	for _, item := range result.Content {
		if text, ok := item.(ai.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func activeWorkers() int {
	codemodeWorkers.mu.Lock()
	defer codemodeWorkers.mu.Unlock()
	return codemodeWorkers.active
}

func waitForActiveWorkers(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for activeWorkers() != want {
		if time.Now().After(deadline) {
			t.Fatalf("%d codemode workers alive, want %d", activeWorkers(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// waitIdle returns once every VM has exited and given its slot back.
func (a *workerAdmission) waitIdle() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for a.active > 0 {
		a.idle.Wait()
	}
}

// Workers still running or shutting down hold their slots across calls: at the limit a script fails at once without
// starting a VM, and freeing one slot admits exactly one more.
func TestCodemodeWorkerAdmissionBoundsSurvivingWorkers(t *testing.T) {
	waitForActiveWorkers(t, 0)
	var releases []func()
	for range maxConcurrentCodemodeWorkers {
		release, err := codemodeWorkers.acquire()
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	released := 0
	t.Cleanup(func() {
		for _, release := range releases[released:] {
			release()
		}
	})

	blocked := runCode(t.Context(), t, "return 3")
	if !blocked.IsError {
		t.Fatalf("blocked run succeeded: %s", resultText(blocked))
	}
	if details, ok := blocked.Details.(ToolDetails); !ok || len(details.Calls) != 0 {
		t.Fatalf("details = %#v, want no calls", blocked.Details)
	}
	if text := resultText(blocked); !strings.Contains(text, "Codemode worker limit reached (2 workers are still running or shutting down)") {
		t.Fatalf("blocked text = %q", text)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	aborted := runCode(ctx, t, "while (true) {}")
	if !aborted.IsError || !strings.Contains(resultText(aborted), "Script aborted:") {
		t.Fatalf("aborted run = %q (error %v)", resultText(aborted), aborted.IsError)
	}

	releases[0]()
	released = 1
	if ok := runCode(t.Context(), t, "return 42"); ok.IsError {
		t.Fatalf("run after a release failed: %s", resultText(ok))
	}
	waitForActiveWorkers(t, 1)
}

// A cancelled VM keeps its slot until it exits, and gives it back when it does.
func TestCodemodeWorkerHoldsItsSlotWhileRunning(t *testing.T) {
	waitForActiveWorkers(t, 0)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan agent.AgentToolResult, 1)
	go func() { done <- runCode(ctx, t, "while (true) {}") }()
	waitForActiveWorkers(t, 1)
	cancel()
	if result := <-done; !result.IsError || !strings.Contains(resultText(result), "Script aborted:") {
		t.Fatalf("result = %q", resultText(result))
	}
	waitForActiveWorkers(t, 0)
}

// Concurrent callers cannot race past the shared limit.
func TestCodemodeWorkerAdmissionAdmitsOnlyTheLimitFromABurst(t *testing.T) {
	waitForActiveWorkers(t, 0)
	var mu sync.Mutex
	var releases []func()
	failures := 0
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			release, err := codemodeWorkers.acquire()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures++
				return
			}
			releases = append(releases, release)
		})
	}
	wg.Wait()
	for _, release := range releases {
		release()
	}
	if len(releases) != maxConcurrentCodemodeWorkers || failures != 6-maxConcurrentCodemodeWorkers {
		t.Fatalf("admitted %d, refused %d; want %d, %d", len(releases), failures, maxConcurrentCodemodeWorkers, 6-maxConcurrentCodemodeWorkers)
	}
}
