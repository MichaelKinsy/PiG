package extensionconformance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestConformance_FileMutationQueueAgreesAcrossSDKs: Pi exports withFileMutationQueue (coding-agent index.ts:389, core/tools/file-mutation-queue.ts) so an extension's edit and
// write tools can serialize their read-modify-write per file. Concurrent tasks on one file run one at a time in call order, tasks on other files run in parallel, a failing
// task still releases the file, aliases of one file (a symlink, a `..` path, a path that does not exist yet or lies below a regular file) share a queue, and a path that cannot be resolved fails without
// running the task. Every SDK must produce the exact start/end log, which is also Pi's own behavior in the Node runtime.
func TestConformance_FileMutationQueueAgreesAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Parallel()

	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	link := filepath.Join(dir, "link.txt")
	loopA, loopB := filepath.Join(dir, "loop-a"), filepath.Join(dir, "loop-b")
	if err := os.WriteFile(a, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	testenv.Symlink(t, a, link)
	testenv.Symlink(t, loopB, loopA)
	testenv.Symlink(t, loopA, loopB)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "not-yet.txt")

	type task = map[string]any
	// The tasks order themselves by progress, not sleeps (see the file_queue_probe fixtures): a task that waits on "started" calls into the queue once that task is inside it;
	// "called" waits for the call, then a settle time lets a task that skipped the queue run before the holder finishes; "holdEnded" keeps a task inside until the others end.
	cases := []struct {
		name  string
		tasks []task
		log   []string
		fails []int
	}{
		{"one file serializes, another runs in parallel", []task{
			{"id": 1, "path": a, "holdEnded": []int{3}, "holdCalled": []int{2}},
			{"id": 2, "path": a, "waitStarted": []int{1}},
			{"id": 3, "path": b, "waitStarted": []int{1}},
		}, []string{"start:1", "start:3", "end:3", "end:1", "start:2", "end:2"}, []int{}},
		{"waiters run in the order they called", []task{
			{"id": 1, "path": a, "holdCalled": []int{3}},
			{"id": 2, "path": a, "waitStarted": []int{1}},
			{"id": 3, "path": a, "waitCalled": []int{2}},
		}, []string{"start:1", "end:1", "start:2", "end:2", "start:3", "end:3"}, []int{}},
		{"a symlink shares its target's queue", []task{
			{"id": 1, "path": a, "holdCalled": []int{2}}, {"id": 2, "path": link, "waitStarted": []int{1}},
		}, []string{"start:1", "end:1", "start:2", "end:2"}, []int{}},
		{"a path with .. shares the queue of its clean form", []task{
			{"id": 1, "path": a, "holdCalled": []int{2}}, {"id": 2, "path": filepath.Join(dir, "sub", "..", "a.txt"), "waitStarted": []int{1}},
		}, []string{"start:1", "end:1", "start:2", "end:2"}, []int{}},
		{"a path that does not exist yet is keyed by its absolute path", []task{
			{"id": 1, "path": missing, "holdCalled": []int{2}}, {"id": 2, "path": missing, "waitStarted": []int{1}},
		}, []string{"start:1", "end:1", "start:2", "end:2"}, []int{}},
		{"a path below a regular file (ENOTDIR) is keyed by its absolute path", []task{
			{"id": 1, "path": filepath.Join(a, "child"), "holdCalled": []int{2}}, {"id": 2, "path": filepath.Join(a, "child"), "waitStarted": []int{1}},
		}, []string{"start:1", "end:1", "start:2", "end:2"}, []int{}},
		{"a failing task releases the file", []task{
			{"id": 1, "path": a, "fail": true, "holdCalled": []int{2}}, {"id": 2, "path": a, "waitStarted": []int{1}},
		}, []string{"start:1", "end:1", "start:2", "end:2"}, []int{1}},
		{"a path that cannot be resolved fails without running the task", []task{
			{"id": 1, "path": loopA},
		}, []string{}, []int{1}},
	}

	runCase := func(t *testing.T, h *harness, tasks []task) (log []string, errs []int) {
		t.Helper()
		args, err := json.Marshal(map[string]any{"settleMs": 200, "tasks": tasks})
		if err != nil {
			t.Fatal(err)
		}
		before := len(*h.notify)
		// A task stuck in the queue (a chain never released) blocks the command; the deadline turns that into a failure instead of a hang.
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		if !h.runner.ExecuteCommand(ctx, "file_queue_probe", string(args)) {
			t.Fatal("fixture has no file_queue_probe command")
		}
		var reported string
		pollUntilConformance(t, "file_queue_probe: no report", func() bool {
			for _, n := range (*h.notify)[before:] {
				if rest, ok := strings.CutPrefix(n, "file_queue:"); ok {
					reported = strings.TrimSuffix(strings.TrimSuffix(rest, ":info"), ":end")
					return true
				}
			}
			return false
		})
		var got struct {
			Log    []string `json:"log"`
			Errors []int    `json:"errors"`
		}
		if err := json.Unmarshal([]byte(reported), &got); err != nil {
			t.Fatalf("%s is not JSON: %v", reported, err)
		}
		return got.Log, got.Errors
	}

	for _, tc := range append(sdkHarnessCases(), packedSDKHarnessCases()...) {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			for _, c := range cases {
				log, errs := runCase(t, h, c.tasks)
				if log == nil {
					log = []string{}
				}
				if errs == nil {
					errs = []int{}
				}
				if !reflect.DeepEqual(log, c.log) || !reflect.DeepEqual(errs, c.fails) {
					t.Errorf("%s: log %v errors %v, want log %v errors %v", c.name, log, errs, c.log, c.fails)
				}
			}
		})
	}
}
