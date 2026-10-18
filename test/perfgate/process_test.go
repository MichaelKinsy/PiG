// SPDX-License-Identifier: MIT

// The ceilings are measured on linux; other platforms allocate differently on their file, terminal and process paths.

//go:build linux

package perfgate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/perfgate"
	"github.com/MichaelKinsy/PiG/internal/profiling"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

var pigBinary = sync.OnceValues(func() (string, error) {
	root, err := testenv.ModuleRootPath()
	if err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp("", "pig-perfgate-")
	if err != nil {
		return "", err
	}
	out := filepath.Join(dir, "pig")
	if strings.HasSuffix(os.Args[0], ".exe") {
		out += ".exe"
	}
	build := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-o", out, "./cmd/pig")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if data, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build ./cmd/pig: %w\n%s", err, data)
	}
	return out, nil
})

func TestMain(m *testing.M) {
	code := m.Run()
	if path, err := pigBinary(); err == nil {
		_ = os.RemoveAll(filepath.Dir(path))
	}
	os.Exit(code)
}

// pigHome is an isolated home for one gate: HOME, the config roots and the agent directories are temporary, and the inherited PI_/PIG_ variables and Go runtime tuning are dropped, so the counts depend only on the binary and its inputs.
type pigHome struct {
	t   *testing.T
	env []string
	cwd string
	// tmp is the process's TMPDIR. Entries left in it after a run are temporary files the process did not remove.
	tmp string
	// tempLeft is the number of entries the last run left in tmp.
	tempLeft int
	// runs counts the runs, to name the profiles PERFGATE_PROFILE_DIR keeps.
	runs int
}

func newPigHome(t *testing.T) *pigHome {
	t.Helper()
	root := t.TempDir()
	home, agent, cwd, tmp := filepath.Join(root, "home"), filepath.Join(root, "agent"), filepath.Join(root, "cwd"), filepath.Join(root, "tmp")
	for _, dir := range []string{home, agent, cwd, tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch {
		case strings.HasPrefix(name, "PI_"), strings.HasPrefix(name, "PIG_"), name == "GOGC", name == "GOMEMLIMIT", name == "GODEBUG", name == "GOMAXPROCS", name == "HOME", name == "USERPROFILE", name == "TMPDIR", name == "TMP", name == "TEMP":
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"HOME="+home, "USERPROFILE="+home,
		"PIG_HOME="+filepath.Join(home, ".pig"), "PI_HOME="+filepath.Join(home, ".pi"),
		"PIG_CODING_AGENT_DIR="+agent, "PI_CODING_AGENT_DIR="+agent,
		"TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp,
		"PIG_TEST_FAUX=1", "GOMAXPROCS=1",
	)
	return &pigHome{t: t, env: env, cwd: cwd, tmp: tmp}
}

// run starts pig with args and returns its exit record. drive, when set, owns stdin and stdout until it returns; stdin is closed afterwards.
func (h *pigHome) run(args []string, drive func(pid int, stdin io.Writer, stdout *bufio.Reader)) profiling.ProcessMetrics {
	h.t.Helper()
	binary, err := pigBinary()
	if err != nil {
		h.t.Fatal(err)
	}
	return h.runBinary(binary, nil, args, drive)
}

// runBinary is run with another binary, such as memProbeBinary, and extra environment entries.
func (h *pigHome) runBinary(binary string, env []string, args []string, drive func(pid int, stdin io.Writer, stdout *bufio.Reader)) profiling.ProcessMetrics {
	h.t.Helper()
	profileDir := h.t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), testbudget.Wait(h.t))
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = h.cwd
	command.Env = append(append(append([]string(nil), h.env...), env...), "PIG_PROFILE=metrics", "PIG_PROFILE_DIR="+profileDir)
	// PERFGATE_PROFILE_DIR keeps an allocation profile of every run, with every allocation sampled, as <test>-<run>-allocs.pprof: compare two runs with go tool pprof -base. Sampling does not change the gated counts.
	keep := os.Getenv("PERFGATE_PROFILE_DIR")
	if keep != "" {
		command.Env = append(command.Env, "PIG_PROFILE=metrics,allocs", "GODEBUG=memprofilerate=1")
	}
	var stderr strings.Builder
	command.Stderr = &stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		h.t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		h.t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	if drive != nil {
		drive(command.Process.Pid, stdin, reader)
	}
	_ = stdin.Close()
	_, _ = io.Copy(io.Discard, reader)
	if err := command.Wait(); err != nil {
		h.t.Fatalf("pig %s: %v\n%s", strings.Join(args, " "), err, stderr.String())
	}
	left, err := os.ReadDir(h.tmp)
	if err != nil {
		h.t.Fatal(err)
	}
	h.tempLeft = len(left)
	for _, entry := range left {
		h.t.Logf("pig %s left %s in TMPDIR", strings.Join(args, " "), entry.Name())
	}
	if keep != "" {
		h.runs++
		profiles, _ := filepath.Glob(filepath.Join(profileDir, "pig-*-allocs.pprof"))
		for _, profile := range profiles {
			data, err := os.ReadFile(profile)
			if err != nil {
				h.t.Fatal(err)
			}
			name := fmt.Sprintf("%s-%d-allocs.pprof", strings.ReplaceAll(h.t.Name(), "/", "_"), h.runs)
			if err := os.WriteFile(filepath.Join(keep, name), data, 0o600); err != nil {
				h.t.Fatal(err)
			}
		}
	}
	matches, _ := filepath.Glob(filepath.Join(profileDir, "pig-*-metrics.json"))
	if len(matches) != 1 {
		h.t.Fatalf("pig %s wrote no metrics record (%v)\n%s", strings.Join(args, " "), matches, stderr.String())
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		h.t.Fatal(err)
	}
	var record profiling.ProcessMetrics
	if err := json.Unmarshal(data, &record); err != nil {
		h.t.Fatalf("%s: %v", matches[0], err)
	}
	return record
}

// rpcTurns drives turns prompts through RPC mode, each to its agent_settled event.
func rpcTurns(t *testing.T, turns int) func(int, io.Writer, *bufio.Reader) {
	return func(_ int, stdin io.Writer, stdout *bufio.Reader) {
		for i := range turns {
			if _, err := fmt.Fprintf(stdin, `{"id":"p%d","type":"prompt","message":"What is 20+22?"}`+"\n", i); err != nil {
				t.Fatal(err)
			}
			for {
				line, err := stdout.ReadBytes('\n')
				if err != nil {
					t.Fatalf("turn %d: RPC output ended before agent_settled: %v", i, err)
				}
				var event struct {
					Type string `json:"type"`
				}
				if json.Unmarshal(line, &event) == nil && event.Type == "agent_settled" {
					break
				}
			}
		}
	}
}

// checkExitState gates what a finished pig still holds when it exits (goroutines, descriptors and child processes) and the temporary entries it left behind. Descriptors count against the process's own start, so descriptors a parent lets it inherit do not count. A field the platform cannot report is -1 and is not gated. Goroutines and descriptors are gated only when the run settled its work before exit: a process that exits during startup, as Pi's process.exit does, still has its background model tasks reading credentials.
func checkExitState(t *testing.T, prefix string, record profiling.ProcessMetrics, tempLeft int, settled bool) {
	t.Helper()
	perfgate.Check(t, prefix+".temp_entries_at_exit", float64(tempLeft))
	if settled {
		perfgate.Check(t, prefix+".goroutines_at_exit", float64(record.Goroutines))
		if record.OpenFDs >= 0 && record.OpenFDsAtStart >= 0 {
			perfgate.Check(t, prefix+".fds_left_open", float64(record.OpenFDs-record.OpenFDsAtStart))
		}
	}
	if record.ChildProcesses >= 0 {
		perfgate.Check(t, prefix+".children_at_exit", float64(record.ChildProcesses))
	}
}

var printArgs = []string{"-ne", "--no-session", "--model", "test-faux/faux-1", "-p", "What is 20+22?"}

// One print-mode round trip with the faux provider and no extensions, from process start to exit.
func TestPrintRoundTripProcessGate(t *testing.T) {
	home := newPigHome(t)
	home.run(printArgs, nil) // The first start in a new home materializes caches; gate the next one.
	record := home.run(printArgs, nil)
	t.Logf("perfgate: process.print record %+v", record)
	perfgate.Check(t, "process.print.allocs", float64(record.AllocObjects))
	perfgate.Check(t, "process.print.bytes", float64(record.AllocBytes))
	perfgate.Check(t, "process.print.gc_cycles", float64(record.GCCycles))
	perfgate.Check(t, "process.print.heap_live_bytes", float64(record.HeapLiveBytes))
	if record.MaxRSSBytes > 0 {
		perfgate.Check(t, "process.print.max_rss_bytes", float64(record.MaxRSSBytes))
	}
	checkExitState(t, "process.print", record, home.tempLeft, true)
}

// heapLiveRuns is the number of (2-turn, 22-turn) pairs behind the heap_live_growth_bytes gate (lead ruling 2026-10-11 15:33Z): one pair's live heap spreads 5-7 KB between runs, which is more than the gate's ceiling.
const heapLiveRuns = 16

// heapLiveGrowthSamples returns the per-turn live-heap growth of heapLiveRuns pairs (the first is small/large already run by the caller) and, for each pair, the A/A spread: the difference between its
// few-turn run and a second few-turn run of the same program, per turn.
func heapLiveGrowthSamples(home *pigHome, args []string, few, many int, small, large profiling.ProcessMetrics) (growth, floor []float64) {
	turns := float64(many - few)
	for i := range heapLiveRuns {
		if i > 0 {
			small = home.run(args, rpcTurns(home.t, few))
			large = home.run(args, rpcTurns(home.t, many))
		}
		copyOfSmall := home.run(args, rpcTurns(home.t, few))
		growth = append(growth, (float64(large.HeapLiveBytes)-float64(small.HeapLiveBytes))/turns)
		floor = append(floor, math.Abs(float64(copyOfSmall.HeapLiveBytes)-float64(small.HeapLiveBytes))/turns)
	}
	return growth, floor
}

// medianOf is the median of values (the mean of the two middle values for an even count); 0 for none. It does not reorder values.
func medianOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	if mid := len(sorted) / 2; len(sorted)%2 == 1 {
		return sorted[mid]
	} else {
		return (sorted[mid-1] + sorted[mid]) / 2
	}
}

// gatedGrowth is the value the ratchet sees: the median growth (never negative), except that a median above the ceiling by no more than the A/A floor is no measurable difference and is gated as the ceiling.
func gatedGrowth(median, floor, ceiling float64) float64 {
	median = max(0, median)
	if median > ceiling && median-floor <= ceiling {
		return ceiling
	}
	return median
}

// RPC mode started and stopped with no prompt, and the cost each further turn adds: allocations, bytes, and the live heap a turn leaves behind.
func TestRPCTurnProcessGate(t *testing.T) {
	home := newPigHome(t)
	args := []string{"-ne", "--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	home.run(args, nil)
	idle := home.run(args, nil)
	perfgate.Check(t, "process.rpc_startup.allocs", float64(idle.AllocObjects))
	perfgate.Check(t, "process.rpc_startup.bytes", float64(idle.AllocBytes))
	checkExitState(t, "process.rpc_startup", idle, home.tempLeft, false)

	const few, many = 2, 22
	small := home.run(args, rpcTurns(t, few))
	large := home.run(args, rpcTurns(t, many))
	tempLeft := home.tempLeft
	turns := float64(many - few)
	t.Logf("perfgate: process.rpc_turn %d turns %+v; %d turns %+v", few, small, many, large)
	perfgate.Check(t, "process.rpc_turn.allocs", counterDelta(large.AllocObjects, small.AllocObjects)/turns)
	perfgate.Check(t, "process.rpc_turn.bytes", counterDelta(large.AllocBytes, small.AllocBytes)/turns)
	growth, floor := heapLiveGrowthSamples(home, args, few, many, small, large)
	median, spread := medianOf(growth), medianOf(floor)
	t.Logf("perfgate: process.rpc_turn.heap_live_growth_bytes median of %d runs %.1f B/turn, A/A floor %.1f B/turn (median |2-turn run - its copy|/turn)", len(growth), median, spread)
	perfgate.Check(t, "process.rpc_turn.heap_live_growth_bytes", gatedGrowth(median, spread, perfgate.Ceiling(t, "process.rpc_turn.heap_live_growth_bytes")))
	if large.MaxRSSBytes > 0 {
		perfgate.Check(t, "process.rpc_turn.max_rss_bytes_at_22_turns", float64(large.MaxRSSBytes))
	}
	checkExitState(t, "process.rpc_turn", large, tempLeft, true)
}

// RSS and PSS at ready, the memory metric the Rust port rpi publishes: RPC mode is ready when it has answered its first command (get_state). Peak RSS over the run is gated beside it.
func TestRPCReadyMemoryGate(t *testing.T) {
	home := newPigHome(t)
	args := []string{"-ne", "--no-session", "--mode", "rpc", "--model", "test-faux/faux-1"}
	home.run(args, nil)
	var rss, pss float64
	record := home.run(args, func(pid int, stdin io.Writer, stdout *bufio.Reader) {
		if _, err := io.WriteString(stdin, `{"id":"ready","type":"get_state"}`+"\n"); err != nil {
			t.Fatal(err)
		}
		for {
			line, err := stdout.ReadBytes('\n')
			if err != nil {
				t.Fatalf("RPC output ended before the get_state response: %v", err)
			}
			var response struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			}
			if json.Unmarshal(line, &response) == nil && response.Type == "response" && response.ID == "ready" {
				break
			}
		}
		rss, pss = perfgate.ProcessMemory(t, pid)
	})
	t.Logf("perfgate: process.rpc_ready rss %.0f pss %.0f peak %d", rss, pss, record.MaxRSSBytes)
	perfgate.Check(t, "process.rpc_ready.rss_bytes", rss)
	perfgate.Check(t, "process.rpc_ready.pss_bytes", pss)
	perfgate.Check(t, "process.rpc_ready.max_rss_bytes", float64(record.MaxRSSBytes))
}
