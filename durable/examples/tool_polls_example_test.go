// Ports packages/durable/test/examples/41-tool-polls.ts: a tool that polls a slow job without holding a process: it parks
// on time between polls, with a backoff, keeping its poll count in the park's checkpoint. A host that wakes the process
// itself sees only the next deadline. The script's printing is the transcript this test reads.
//
// Go mapping: the host's "alarm" loop acts on a deadline only once it lies beyond the clock the alarm last set, so a
// Wake read before the Harness took in the resume is not answered twice (upstream's event loop turn makes that read
// impossible); OnWake runs in goroutines of its own, so its calls are compared as a set of deadlines.

package examples_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/durable/storage"
)

type pollCheckpoint struct {
	Id      string  `json:"id"`
	Polls   int     `json:"polls"`
	DelayMs float64 `json:"delayMs"`
}

// Pi: packages/durable/src/harness/parks.ts (sleep parks), packages/durable/src/harness/tool.ts (durationMs).
func TestExample41ToolPolls(t *testing.T) {
	var script printed
	// The Harness clock, moved by the host's "alarm" below.
	var clockMu sync.Mutex
	clock := 0.0
	now := func() float64 {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}

	// A slow job service: starting twice with one idempotency key starts one job, done on its fourth status check.
	var jobsMu sync.Mutex
	checks := 0
	started := map[string]string{}
	startJob := func(idempotencyKey string) string {
		jobsMu.Lock()
		defer jobsMu.Unlock()
		if _, found := started[idempotencyKey]; !found {
			started[idempotencyKey] = fmt.Sprintf("job-%d", len(started)+7)
		}
		return started[idempotencyKey]
	}
	jobStatus := func() string {
		jobsMu.Lock()
		defer jobsMu.Unlock()
		checks++
		if checks >= 4 {
			return "done"
		}
		return "running"
	}

	waitForJob := &durable.ToolRegistration{
		Name:        "wait_for_job",
		Description: "Start a job and wait until it is done",
		Parameters: map[string]any{
			"type":       "object",
			"properties": map[string]any{"name": map[string]any{"type": "string"}},
			"required":   []any{"name"},
		},
		Replay: durable.ReplaySafe,
		Execute: func(ctx context.Context, _ any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
			checkpoint, err := checkpointOf[pollCheckpoint](api)
			if err != nil {
				return durable.ToolExecutionResult{}, err
			}
			// The first run starts the job under its own idempotency key, memoized first, so a rerun after a crash before the
			// first park finds the same job.
			id := checkpoint.Id
			if id == "" {
				key, err := api.MemoCandidate(ctx, "job.key", uuid.NewString())
				if err != nil {
					return durable.ToolExecutionResult{}, err
				}
				id = startJob(key.(string))
			}
			status := jobStatus()
			polls := checkpoint.Polls + 1
			script.add(fmt.Sprintf("  [poll %d at %vs] %s is %s", polls, now()/1000, id, status))
			if status == "done" {
				return durable.ToolExecutionResult{Output: []ai.ToolResultMessageContent{ai.TextContent{Text: fmt.Sprintf("%s done after %d polls", id, polls)}}}, nil
			}
			// Back off: 1s, 2s, 4s, ... up to a minute.
			previous := checkpoint.DelayMs
			if previous == 0 {
				previous = 500
			}
			delayMs := min(previous*2, 60_000)
			until := api.Now() + delayMs
			return durable.ToolExecutionResult{Park: &durable.ToolPark{Sleep: &durable.ToolParkSleep{
				Checkpoint: delta.JsonObjectOf("id", id, "polls", float64(polls), "delayMs", delayMs), HasCheckpoint: true, Until: &until,
			}}}, nil
		},
	}

	models := fauxModels(
		fauxToolTurn("wait_for_job", map[string]any{"name": "nightly"}, "job-call"),
		fauxAnswer("The nightly job is done."),
	)

	// The host's alarm: `OnWake` says when the Session next needs a process; the "alarm" fires by moving the clock there.
	var wakesMu sync.Mutex
	var wakes []harness.Wake
	registry := harness.CreateRegistry()
	installed(t, registry, &durable.Extension{Name: "jobs", Tools: []*durable.ToolRegistration{waitForJob}})
	opened, err := harness.OpenHarness(background, storage.NewMemoryStorage(), harness.HarnessOptions{
		Models: models, Registry: registry, Now: now,
		OnWake: func(wake harness.Wake) {
			wakesMu.Lock()
			defer wakesMu.Unlock()
			wakes = append(wakes, wake)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := must(opened.Root(background, &harness.RootOptions{Agent: &harness.AgentChange{Model: harness.SetTo(fauxModel)}}))
	submission := must(root.Submit(background, durable.SubmissionDraft{Type: durable.SubmissionTypeInput, Content: ai.UserText("Run the nightly job")}))

	settled := make(chan struct{})
	go func() {
		defer close(settled)
		if _, err := submission.Wait(background); err != nil {
			t.Error(err)
		}
	}()
	timeout := time.After(30 * time.Second)
loop:
	for {
		select {
		case <-settled:
			break loop
		case <-timeout:
			t.Fatalf("the call did not end; script so far: %q", script.all())
		case <-time.After(5 * time.Millisecond):
		}
		wake := opened.Wake()
		// Parked between polls: nothing runs, only the next poll time is set.
		if !wake.Active && wake.At != nil && *wake.At > now() {
			script.add(fmt.Sprintf("asleep until %v s: %s", *wake.At/1000, wakeText(wake)))
			clockMu.Lock()
			clock = *wake.At
			clockMu.Unlock()
			opened.Resume()
		}
	}
	result := newestToolResultMessage(t, root, 10)
	script.add("result: " + partsText(result.Content))
	durationMs := "undefined"
	if result.DurationMs != nil {
		durationMs = fmt.Sprint(*result.DurationMs)
	}
	script.add("durationMs (first run to result, on the Harness clock): " + durationMs)

	expectEqual(t, "script", script.all(), []string{
		"  [poll 1 at 0s] job-7 is running",
		`asleep until 1 s: {"active":false,"at":1000}`,
		"  [poll 2 at 1s] job-7 is running",
		`asleep until 3 s: {"active":false,"at":3000}`,
		"  [poll 3 at 3s] job-7 is running",
		`asleep until 7 s: {"active":false,"at":7000}`,
		"  [poll 4 at 7s] job-7 is done",
		"result: job-7 done after 4 polls",
		"durationMs (first run to result, on the Harness clock): 7000",
	})
	// The host was told each deadline it had to arm an alarm for, and nothing else with a deadline.
	var deadlines []float64
	wakesMu.Lock()
	for _, wake := range wakes {
		if wake.At != nil && !slices.Contains(deadlines, *wake.At) {
			deadlines = append(deadlines, *wake.At)
		}
	}
	wakesMu.Unlock()
	slices.Sort(deadlines)
	expectEqual(t, "onWake deadlines", deadlines, []float64{1000, 3000, 7000})
	if err := opened.Close(background); err != nil {
		t.Fatal(err)
	}
}
