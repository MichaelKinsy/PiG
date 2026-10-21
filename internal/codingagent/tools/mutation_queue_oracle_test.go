package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

type mutationQueueCall struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Throws bool   `json:"throws"`
}

type mutationQueueScenario struct {
	Dir      string              `json:"dir"`
	Calls    []mutationQueueCall `json:"calls"`
	Releases []int               `json:"releases"`
}

type mutationQueueOutcome struct {
	Counts   []int             `json:"counts"`
	Log      []string          `json:"log"`
	Outcomes map[string]string `json:"outcomes"`
}

// withFileMutationQueue (core/tools/file-mutation-queue.ts) against pinned Pi over 120 scripted runs of two to seven calls issued at once on paths that name the same file through relative forms, a symlink, a directory symlink and a dotdot, different files, a missing file, a broken symlink, a path below a file (ENOTDIR), a symlink loop (ELOOP) and an unreadable directory (EACCES): each callback logs its start, waits for its gate and logs its end or throws, and the gates open in a random order. Compared: the order of events on each file, the number of events after every release, and each call's outcome (the result, the thrown error, or the errno of a path that cannot be resolved).
func TestWithFileMutationQueueMatchesPi(t *testing.T) {
	root := t.TempDir()
	rng := rand.New(rand.NewPCG(37, 12))
	layout := func(dir string) {
		for _, name := range []string{"a", "b"} {
			if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		for _, directory := range []string{"d", "locked"} {
			if err := os.Mkdir(filepath.Join(dir, directory), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "d", "c"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		for link, target := range map[string]string{"la": "a", "lb": "b", "dl": "d", "bl": "nowhere", "ll": "ll"} {
			testenv.Symlink(t, target, filepath.Join(dir, link))
		}
		if err := os.Chmod(filepath.Join(dir, "locked"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(dir, "locked"), 0o755) })
	}
	// Each path's queue: paths with the same group share one; an empty group cannot be resolved and fails.
	groups := map[string]string{"a": "A", "./a": "A", "la": "A", "d/../a": "A", "b": "B", "lb": "B", "dl/c": "C", "d/c": "C", "bl": "BL", "nowhere": "N", "a/x": "AX", "new.txt": "NEW", "ll": "", "locked/x": ""}
	paths := slices.Sorted(func(yield func(string) bool) {
		for path := range groups {
			if !yield(path) {
				return
			}
		}
	})
	var scenarios []mutationQueueScenario
	for i := range 120 {
		dir := filepath.Join(root, fmt.Sprint(i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		layout(dir)
		scenario := mutationQueueScenario{Dir: dir}
		for k := range 2 + rng.IntN(6) {
			scenario.Calls = append(scenario.Calls, mutationQueueCall{Path: paths[rng.IntN(len(paths))], Name: fmt.Sprintf("c%d", k), Throws: rng.IntN(4) == 0})
		}
		scenario.Releases = rng.Perm(len(scenario.Calls))
		scenarios = append(scenarios, scenario)
	}
	want := piMutationQueueOutcomes(t, scenarios)
	mismatches, queued, failed := 0, 0, 0
	for i, scenario := range scenarios {
		t.Chdir(scenario.Dir)
		got := runMutationQueueScenario(t, scenario, want[i].Counts, groups)
		// Per file the events keep their order; across files the order is the scheduler's.
		project := func(log []string) map[string][]string {
			out := map[string][]string{}
			for _, event := range log {
				verb, name, _ := strings.Cut(event, " ")
				for _, call := range scenario.Calls {
					if call.Name == name {
						out[groups[call.Path]] = append(out[groups[call.Path]], verb+" "+name)
					}
				}
			}
			return out
		}
		if !reflect.DeepEqual(project(got.Log), project(want[i].Log)) || !reflect.DeepEqual(got.Outcomes, want[i].Outcomes) || !reflect.DeepEqual(got.Counts, want[i].Counts) {
			if mismatches++; mismatches <= 4 {
				t.Errorf("scenario %d calls %+v releases %v:\n Go counts %v log %v outcomes %v\n Pi counts %v log %v outcomes %v", i, scenario.Calls, scenario.Releases, got.Counts, got.Log, got.Outcomes, want[i].Counts, want[i].Log, want[i].Outcomes)
			}
		}
		for _, outcome := range want[i].Outcomes {
			if strings.HasPrefix(outcome, "err E") {
				failed++
			}
		}
		queued += len(want[i].Log)
	}
	if mismatches > 4 {
		t.Errorf("%d of %d runs differ from Pi", mismatches, len(scenarios))
	}
	if failed < 30 || queued < 600 {
		t.Errorf("thin coverage: %d unresolvable paths, %d events", failed, queued)
	}
}

// piMutationQueueOutcomes runs scenarios through the pinned Pi's withFileMutationQueue (testdata/mutation_queue.mjs) and returns what it did.
func piMutationQueueOutcomes(t *testing.T, scenarios []mutationQueueScenario) []mutationQueueOutcome {
	t.Helper()
	input, err := json.Marshal(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/mutation_queue.mjs")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "node", script, pigversion.UpstreamPackageVersion)
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
	}
	var want []mutationQueueOutcome
	if err := json.Unmarshal(output, &want); err != nil {
		t.Fatal(err)
	}
	return want
}

// runMutationQueueScenario issues the calls in order, each only after the previous one has taken its place in the queue, opens the gates in the scripted order, and waits after each release for the event count Pi reached.
func runMutationQueueScenario(t *testing.T, scenario mutationQueueScenario, counts []int, groups map[string]string) mutationQueueOutcome {
	t.Helper()
	var mu sync.Mutex
	var log []string
	outcomes := map[string]string{}
	gates := make([]chan struct{}, len(scenario.Calls))
	var wg sync.WaitGroup
	logLen := func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(log)
	}
	waitFor := func(condition func() bool, what string) {
		deadline := time.Now().Add(10 * time.Second)
		for !condition() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			runtime.Gosched()
			time.Sleep(50 * time.Microsecond)
		}
	}
	for i, call := range scenario.Calls {
		gates[i] = make(chan struct{})
		key, keyErr := canonicalKey(call.Path)
		processFileMutationQueue.mu.Lock()
		before := processFileMutationQueue.chains[key]
		processFileMutationQueue.mu.Unlock()
		done := make(chan struct{})
		wg.Go(func() {
			defer close(done)
			value, err := WithFileMutationQueue(call.Path, func() (string, error) {
				mu.Lock()
				log = append(log, "start "+call.Name)
				mu.Unlock()
				<-gates[i]
				mu.Lock()
				log = append(log, "end "+call.Name)
				mu.Unlock()
				if call.Throws {
					return "", errors.New("boom " + call.Name)
				}
				return call.Name, nil
			})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				outcomes[call.Name] = "ok " + value
			case nodeErrorCode(err) != "":
				outcomes[call.Name] = "err " + nodeErrorCode(err)
			default:
				outcomes[call.Name] = "err " + err.Error()
			}
		})
		if keyErr != nil {
			<-done
			continue
		}
		waitFor(func() bool {
			processFileMutationQueue.mu.Lock()
			defer processFileMutationQueue.mu.Unlock()
			current, present := processFileMutationQueue.chains[key]
			// The chain entry is dropped when its holder finishes with nobody behind it, so a call that finished already also counts as registered.
			return (present && current != before) || (!present && before == nil && finished(done))
		}, "the call to register")
	}
	got := mutationQueueOutcome{Counts: []int{}}
	// Pi's count after issuing everything is the number of calls that started at once.
	waitFor(func() bool { return logLen() >= counts[0] }, "the initial starts")
	got.Counts = append(got.Counts, logLen())
	for step, index := range scenario.Releases {
		close(gates[index])
		waitFor(func() bool { return logLen() >= counts[step+1] }, fmt.Sprintf("events after release %d", step))
		got.Counts = append(got.Counts, logLen())
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	got.Log, got.Outcomes = log, outcomes
	return got
}

func finished(done chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
