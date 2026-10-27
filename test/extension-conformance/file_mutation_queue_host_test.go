package extensionconformance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/codingagent/tools"
)

// Pi has ONE withFileMutationQueue (core/tools/file-mutation-queue.ts: a module-level map) that the built-in edit and write tools and every extension share. PiG's
// extension SDKs run in processes of their own, so the queue is the host's (the one the built-in tools use) and each SDK asks it for the file's turn over the wire.
// An SDK that kept a private queue would admit its task at once while a built-in tool (or an extension in another process) holds the file, and would leave the
// host's queue free while its own task holds the file: a lost update, which these rows observe from outside. The host's side of "held" is read through
// the host's own queue, a value no SDK default can fake: a host mutation either runs at once or waits.

// queueProbeRun is one file_queue_probe command, run on its own goroutine so the test can watch the host's queue while the command is inside it.
type queueProbeRun struct {
	started  time.Time
	finished time.Time
	done     chan struct{}
	log      []string
	errs     []int
	failure  string
}

// startQueueProbe runs the fixture's file_queue_probe command with the tasks (see TestConformance_FileMutationQueueAgreesAcrossSDKs for their fields).
func startQueueProbe(t *testing.T, h *harness, settleMs int, tasks ...map[string]any) *queueProbeRun {
	t.Helper()
	args, err := json.Marshal(map[string]any{"settleMs": settleMs, "tasks": tasks})
	if err != nil {
		t.Fatal(err)
	}
	before := len(*h.notify)
	run := &queueProbeRun{started: time.Now(), done: make(chan struct{})}
	go func() {
		defer close(run.done)
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		ok := h.runner.ExecuteCommand(ctx, "file_queue_probe", string(args))
		run.finished = time.Now()
		if !ok {
			run.failure = "fixture has no file_queue_probe command"
			return
		}
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			for _, n := range (*h.notify)[before:] {
				if rest, found := strings.CutPrefix(n, "file_queue:"); found {
					var got struct {
						Log    []string `json:"log"`
						Errors []int    `json:"errors"`
					}
					if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")), &got); err != nil {
						run.failure = fmt.Sprintf("report %q is not JSON: %v", rest, err)
						return
					}
					run.log, run.errs = got.Log, got.Errors
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		run.failure = "file_queue_probe: no report"
	}()
	return run
}

// hostQueueBusy reports whether a mutation of file would have to wait in the host's queue now: it queues an empty mutation behind the current holder and sees
// whether it runs at once. A mutation that had to wait runs later, once the holder is done, and changes nothing.
func hostQueueBusy(t *testing.T, file string) bool {
	t.Helper()
	ran := make(chan struct{})
	go func() {
		_, _ = tools.WithFileMutationQueue(file, func() (struct{}, error) {
			close(ran)
			return struct{}{}, nil
		})
	}()
	select {
	case <-ran:
		return false
	case <-time.After(50 * time.Millisecond):
		return true
	}
}

func awaitHostQueueBusy(t *testing.T, name, file string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !hostQueueBusy(t, file) {
		if time.Now().After(deadline) {
			t.Fatalf("%s: a task inside withFileMutationQueue never held the host's queue for its file: the SDK keeps a private queue", name)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireProbeLog(t *testing.T, name string, run *queueProbeRun, want ...string) {
	t.Helper()
	select {
	case <-run.done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the probe never finished", name)
	}
	if run.failure != "" {
		t.Fatalf("%s: %s", name, run.failure)
	}
	if !slices.Equal(run.log, want) || len(run.errs) != 0 {
		t.Errorf("%s: log %v errors %v, want log %v and no errors", name, run.log, run.errs, want)
	}
}

func queueHarnessCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

func openQueueHarness(t *testing.T, tc harnessCase) *harness {
	t.Helper()
	h := tc.make(t)
	t.Cleanup(func() {
		if h.cleanup != nil {
			h.cleanup()
		}
		if h.host != nil {
			h.host.Shutdown("test done")
		}
	})
	return h
}

// TestConformance_FileMutationQueueIsTheHostsQueue: a mutation the host's built-in tools hold open (here: tools.WithFileMutationQueue, the queue the edit and write
// tools use) keeps an SDK task on the same file waiting until it ends, and an SDK task inside withFileMutationQueue keeps a host mutation of the file waiting, in
// every SDK, isolated, packed and fused. Pi's single queue gives both.
func TestConformance_FileMutationQueueIsTheHostsQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	for _, tc := range queueHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := openQueueHarness(t, tc)
			dir := t.TempDir()

			// The host holds the file: the SDK task waits for it.
			held := filepath.Join(dir, "held.txt")
			inside, release := make(chan struct{}), make(chan struct{})
			holderDone := make(chan struct{})
			go func() {
				defer close(holderDone)
				_, _ = tools.WithFileMutationQueue(held, func() (struct{}, error) {
					close(inside)
					<-release
					return struct{}{}, nil
				})
			}()
			<-inside
			waiter := startQueueProbe(t, h, 100, map[string]any{"id": 1, "path": held})
			select {
			case <-waiter.done:
				close(release)
				t.Fatalf("%s: the SDK task ran while a host mutation held the file: the SDK keeps a private queue", tc.name)
			case <-time.After(1500 * time.Millisecond):
			}
			close(release)
			<-holderDone
			requireProbeLog(t, tc.name+" waiting for the host", waiter, "start:1", "end:1")

			// The SDK task holds the file: the host's queue is busy, and a host mutation waits for the task.
			const settleMs = 2000
			file := filepath.Join(dir, "sdk-held.txt")
			holder := startQueueProbe(t, h, settleMs,
				map[string]any{"id": 1, "path": file, "holdCalled": []int{2}},
				map[string]any{"id": 2, "path": filepath.Join(dir, "other.txt"), "waitStarted": []int{1}})
			awaitHostQueueBusy(t, tc.name, file)
			hostRan := make(chan time.Time, 1)
			go func() {
				_, _ = tools.WithFileMutationQueue(file, func() (struct{}, error) {
					hostRan <- time.Now()
					return struct{}{}, nil
				})
			}()
			select {
			case at := <-hostRan:
				// The task stays inside for settleMs after it started; a host mutation that ran earlier was not held back by it.
				if at.Sub(holder.started) < settleMs*time.Millisecond*9/10 {
					t.Errorf("%s: a host mutation ran %v after the SDK task started, while the task still held the file", tc.name, at.Sub(holder.started))
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s: a host mutation never ran after the SDK task", tc.name)
			}
			requireProbeLog(t, tc.name+" holding for the host", holder, "start:1", "start:2", "end:2", "end:1")
		})
	}
}

// TestConformance_FileMutationQueueSpansExtensionProcesses: two extensions in different processes (or cells) share one queue, as two extensions share Pi's
// module-level map. Each SDK's extension holds a file while the next SDK's extension asks for it; the second one's task must wait out the first one's.
func TestConformance_FileMutationQueueSpansExtensionProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()
	cases := queueHarnessCases()
	harnesses := make([]*harness, len(cases))
	for i, tc := range cases {
		harnesses[i] = openQueueHarness(t, tc)
	}
	dir := t.TempDir()
	const settleMs = 2000
	for i, holderCase := range cases {
		waiterCase := cases[(i+1)%len(cases)]
		t.Run(holderCase.name+"→"+waiterCase.name, func(t *testing.T) {
			file := filepath.Join(dir, fmt.Sprintf("shared-%d.txt", i))
			holder := startQueueProbe(t, harnesses[i], settleMs,
				map[string]any{"id": 1, "path": file, "holdCalled": []int{2}},
				map[string]any{"id": 2, "path": filepath.Join(dir, fmt.Sprintf("other-%d.txt", i)), "waitStarted": []int{1}})
			awaitHostQueueBusy(t, holderCase.name, file)
			waiter := startQueueProbe(t, harnesses[(i+1)%len(cases)], 100, map[string]any{"id": 1, "path": file})
			requireProbeLog(t, waiterCase.name+" behind "+holderCase.name, waiter, "start:1", "end:1")
			if waited := waiter.finished.Sub(holder.started); waited < settleMs*time.Millisecond*9/10 {
				t.Errorf("%s: its task finished %v after %s's task started, while that task still held the file: the SDKs do not share one queue", waiterCase.name, waited, holderCase.name)
			}
			requireProbeLog(t, holderCase.name+" holding", holder, "start:1", "start:2", "end:2", "end:1")
		})
	}
}
