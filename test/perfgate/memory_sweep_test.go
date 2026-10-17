// SPDX-License-Identifier: MIT

//go:build linux

package perfgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/test/longsession"
)

// The memory half of the per-TEST-TIP scaling sweep: the M6 rows at three history sizes, 0, 1x and 10x, so bench/memsweep/memsweep.py can
// tell memory that follows the context from memory that follows the history, and flag retained
// bytes that grow faster than the history. It measures and writes; it gates nothing. It runs only with PIG_MEMSWEEP_OUT set, the JSONL file
// it appends one record per point to.

// memSweepPrompts are the long row's history sizes in prompts: 0, 1x and 10x.
var memSweepPrompts = []int{0, 5, 50}

// memSweepTmuxPrompts are the tmux row's: its base is one prompt, not none, because the idle collection (D172) arms only when a run ends;
// with no run the idle heap is whatever the last start-up collection left, which varies from run to run.
var memSweepTmuxPrompts = []int{1, 5, 50}

// memSweepResumeTurns are the resumed row's history sizes in longsession turns before its compaction, 0, 1x and 10x, about 21 KB of file each.
var memSweepResumeTurns = []int{0, 200, 2000}

// memSweepKeptTurns is the resumed row's context: the turns the compaction keeps. It is the same at every history size.
const memSweepKeptTurns = 8

// memSweepPoint is one record of the sweep's JSONL output.
type memSweepPoint struct {
	Scenario string `json:"scenario"`
	// N is the history size in the scenario's unit: prompts, or for "resume" longsession turns before the compaction.
	N int `json:"n"`
	// HistoryBytes is what the session holds on disk or has sent in total; ContextBytes is the last request body, the context pig sent.
	HistoryBytes int64  `json:"history_bytes"`
	ContextBytes int64  `json:"context_bytes"`
	HeapLive     uint64 `json:"heap_live_bytes"`
	// Nonreleased is /memory/classes/total less /memory/classes/heap/released at idle (tmux only).
	Nonreleased uint64 `json:"go_nonreleased_bytes,omitempty"`
	GoMaxProcs  string `json:"gomaxprocs"`
	// CPUsAllowed is the size of pig's CPU affinity mask just before it exits. With GOMAXPROCS unset and a mask no wider than pig's cap the
	// runtime follows the mask, and a cgroup cpuset update that resets the mask to the whole host leaves a host-wide P set live (about 3 MB
	// at 164 CPUs); the driver drops such points.
	CPUsAllowed int `json:"cpus_allowed"`
}

// memSweepCPUsAllowed counts the CPUs in /proc/<pid>/status's Cpus_allowed_list.
func memSweepCPUsAllowed(t *testing.T, pig *ptyPig) int {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pig.command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	for line := range strings.Lines(string(data)) {
		list, ok := strings.CutPrefix(line, "Cpus_allowed_list:")
		if !ok {
			continue
		}
		count := 0
		for part := range strings.SplitSeq(strings.TrimSpace(list), ",") {
			first, last, isRange := strings.Cut(part, "-")
			if !isRange {
				last = first
			}
			a, errA := strconv.Atoi(first)
			b, errB := strconv.Atoi(last)
			if errA != nil || errB != nil {
				t.Fatalf("Cpus_allowed_list %q", list)
			}
			count += b - a + 1
		}
		return count
	}
	t.Fatal("no Cpus_allowed_list in /proc/<pid>/status")
	return 0
}

func memSweepOut(t *testing.T) string {
	t.Helper()
	out := os.Getenv("PIG_MEMSWEEP_OUT")
	if out == "" {
		t.Skip("PIG_MEMSWEEP_OUT is not set: the memory scaling sweep runs from bench/memsweep/memsweep.py")
	}
	return out
}

// memSweepHome is newPigHome with GOMAXPROCS from PIG_MEMSWEEP_GOMAXPROCS: a number, or "default" to leave it unset so pig's own policy
// applies. Unset keeps newPigHome's 1.
func memSweepHome(t *testing.T) (*pigHome, string) {
	t.Helper()
	home := newPigHome(t)
	procs := os.Getenv("PIG_MEMSWEEP_GOMAXPROCS")
	if procs == "" {
		return home, "1"
	}
	env := home.env[:0]
	for _, entry := range home.env {
		if !strings.HasPrefix(entry, "GOMAXPROCS=") {
			env = append(env, entry)
		}
	}
	home.env = env
	if procs != "default" {
		home.env = append(home.env, "GOMAXPROCS="+procs)
	}
	return home, procs
}

// memSweepStart is startPTY, through taskset -c $PIG_MEMSWEEP_CPUS when that is set: pig's own start pins it again, because a cgroup cpuset
// update can reset the CPU mask the test process inherited from the driver's taskset, and the runtime sizes its startup P set from the
// mask pig starts with. taskset execs pig, so the process is still pig.
func memSweepStart(t *testing.T, home *pigHome, binary string, args []string, cols, rows uint16, extra ...string) *ptyPig {
	t.Helper()
	if cpus := os.Getenv("PIG_MEMSWEEP_CPUS"); cpus != "" {
		args = append([]string{"-c", cpus, binary}, args...)
		binary = "taskset"
	}
	return startPTY(t, home, binary, args, cols, rows, extra...)
}

func memSweepWrite(t *testing.T, out string, point memSweepPoint) {
	t.Helper()
	data, err := json.Marshal(point)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(data, '\n')); err != nil {
		t.Fatal(err)
	}
	t.Logf("memsweep: %s", data)
}

// TestMemoryScalingSweepLong is Pi-Bolt's long row (TestLongSessionMemoryGate) at 0, 5 and 50 prompts, same and distinct contents: the live
// heap after two collections at exit and the last request body. With context equal to history here, what memory bounded by the context
// asks is that the heap less the context stays flat as the prompts grow.
func TestMemoryScalingSweepLong(t *testing.T) {
	out := memSweepOut(t)
	binary, err := pigBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []struct {
		name     string
		distinct bool
	}{{"long_same", false}, {"long_distinct", true}} {
		for _, prompts := range memSweepPrompts {
			t.Run(fmt.Sprintf("%s/%d", variant.name, prompts), func(t *testing.T) {
				home, procs := memSweepHome(t)
				model := newBoltModel(t, variant.distinct, 0)
				writeBoltHome(t, home, model, max(prompts, 1))
				warm := memSweepStart(t, home, binary, boltArgs, 120, 40)
				warm.ready()
				warm.exit()
				pig := memSweepStart(t, home, binary, boltArgs, 120, 40)
				pig.ready()
				for n := 1; n <= prompts; n++ {
					pig.prompt(n, 100*time.Millisecond)
				}
				cpus := memSweepCPUsAllowed(t, pig)
				record := pig.exit()
				sent := int64(model.lastRequestBytes())
				memSweepWrite(t, out, memSweepPoint{Scenario: variant.name, N: prompts, HistoryBytes: sent, ContextBytes: sent, HeapLive: record.HeapLiveBytes, GoMaxProcs: procs, CPUsAllowed: cpus})
			})
		}
	}
}

// TestMemoryScalingSweepTmux is Pi-Bolt's tmux row (TestTmuxIdleMemoryGate) with 1, 5 and 50 prompts before the idle: runtime/metrics of
// the idle process after the idle collection.
func TestMemoryScalingSweepTmux(t *testing.T) {
	out := memSweepOut(t)
	binary, err := memProbeBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, prompts := range memSweepTmuxPrompts {
		t.Run(fmt.Sprint(prompts), func(t *testing.T) {
			home, procs := memSweepHome(t)
			model := newBoltModel(t, false, tmuxModelLatency)
			writeBoltHome(t, home, model, max(prompts, 1))
			warm := memSweepStart(t, home, binary, boltArgs, 160, 48)
			warm.ready()
			warm.exit()
			snapshots := t.TempDir()
			pig := memSweepStart(t, home, binary, boltArgs, 160, 48, "PIG_PERFGATE_SNAPSHOT_DIR="+snapshots)
			pig.ready()
			for i := range 40 {
				pig.send(string("abcdefghij"[i%10]))
				time.Sleep(5 * time.Millisecond)
			}
			pig.settle(200 * time.Millisecond)
			pig.send("\x15")
			pig.settle(200 * time.Millisecond)
			lines := make([]string, 400)
			for i := range lines {
				lines[i] = fmt.Sprintf("line %d: lorem ipsum dolor sit amet", i)
			}
			pig.send("\x1b[200~" + strings.Join(lines, "\r") + "\x1b[201~")
			pig.settle(300 * time.Millisecond)
			pig.send("\x15")
			pig.settle(100 * time.Millisecond)
			pig.send("\x1b")
			pig.settle(300 * time.Millisecond)
			for n := 1; n <= prompts; n++ {
				pig.prompt(n, 300*time.Millisecond)
			}
			pig.settle(idleCollectionWait)
			values := pig.snapshot(snapshots, 1)
			cpus := memSweepCPUsAllowed(t, pig)
			pig.exit()
			sent := int64(model.lastRequestBytes())
			memSweepWrite(t, out, memSweepPoint{
				Scenario: "tmux_idle", N: prompts, HistoryBytes: sent, ContextBytes: sent, GoMaxProcs: procs, CPUsAllowed: cpus,
				HeapLive:    values["/gc/heap/live:bytes"],
				Nonreleased: values["/memory/classes/total:bytes"] - values["/memory/classes/heap/released:bytes"],
			})
		})
	}
}

// TestMemoryScalingSweepResume opens a session file of 0, 200 or 2000 longsession turns followed by a compaction that keeps the last
// memSweepKeptTurns turns, answers one prompt in it, and exits: the context is the same at every size and only the history grows, so the
// heap at exit should not follow the history (M7).
func TestMemoryScalingSweepResume(t *testing.T) {
	out := memSweepOut(t)
	binary, err := pigBinary()
	if err != nil {
		t.Fatal(err)
	}
	for _, history := range memSweepResumeTurns {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			home, procs := memSweepHome(t)
			model := newBoltModel(t, false, 0)
			writeBoltHome(t, home, model, 1)
			path, historyBytes := memSweepResumeFixture(t, home.cwd, history, memSweepKeptTurns)
			args := []string{"--session", path, "--model", "fake/fake-model"}
			warm := memSweepStart(t, home, binary, boltArgs, 120, 40)
			warm.ready()
			warm.exit()
			pig := memSweepStart(t, home, binary, args, 120, 40)
			pig.ready()
			since := pig.mark()
			pig.send(boltPrompt)
			time.Sleep(50 * time.Millisecond)
			pig.send("\r")
			pig.waitFor(since, "Done: read all four files", 100*time.Millisecond)
			cpus := memSweepCPUsAllowed(t, pig)
			record := pig.exit()
			memSweepWrite(t, out, memSweepPoint{Scenario: "resume", N: history, HistoryBytes: historyBytes, ContextBytes: int64(model.lastRequestBytes()), HeapLive: record.HeapLiveBytes, GoMaxProcs: procs, CPUsAllowed: cpus})
		})
	}
}

// memSweepResumeFixture writes dir/session-<history>.jsonl: the header, history+kept longsession turns, then a compaction whose kept range is
// the last kept turns (internal/codingagent's resumeFixture for M7). It returns the path and the bytes of the turns before the kept range.
func memSweepResumeFixture(t *testing.T, dir string, history, kept int) (string, int64) {
	t.Helper()
	turns := history + kept
	path := filepath.Join(dir, fmt.Sprintf("session-%d.jsonl", history))
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(file, 1<<16)
	lines := &memSweepLines{out: w, keepFrom: 1 + 4*history, limit: 1 + 4*turns}
	// The token target overshoots (a turn is about 4.6k tokens); the line limit cuts the file at the turn boundary.
	if _, err := longsession.Generate(lines, longsession.Options{Tokens: turns * 8192, Window: 200000, Seed: 7, Cwd: dir}); err != nil {
		t.Fatal(err)
	}
	if lines.lines != lines.limit {
		t.Fatalf("fixture has %d lines; want %d", lines.lines, lines.limit)
	}
	compaction, err := json.Marshal(struct {
		Type             string `json:"type"`
		ID               string `json:"id"`
		ParentID         string `json:"parentId"`
		Timestamp        string `json:"timestamp"`
		Summary          string `json:"summary"`
		FirstKeptEntryID string `json:"firstKeptEntryId"`
		TokensBefore     int    `json:"tokensBefore"`
		FromHook         bool   `json:"fromHook"`
	}{"compaction", "c0", fmt.Sprintf("b%d", turns-1), "2027-01-01T00:00:00.000Z", "The session read and explained the modules so far.",
		fmt.Sprintf("u%d", history), turns * 4600, false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(append(compaction, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path, lines.bytes - lines.keptBytes
}

// memSweepLines passes the first limit lines written to it to out and counts their bytes, and the bytes of the lines from keepFrom on.
type memSweepLines struct {
	out                    *bufio.Writer
	keepFrom, limit, lines int
	bytes, keptBytes       int64
}

func (c *memSweepLines) Write(p []byte) (int, error) {
	for start := 0; start < len(p); {
		end := start
		for end < len(p) && p[end] != '\n' {
			end++
		}
		if end < len(p) {
			end++
		}
		if c.lines < c.limit {
			if _, err := c.out.Write(p[start:end]); err != nil {
				return 0, err
			}
			c.bytes += int64(end - start)
			if c.lines >= c.keepFrom {
				c.keptBytes += int64(end - start)
			}
			if p[end-1] == '\n' {
				c.lines++
			}
		}
		start = end
	}
	return len(p), nil
}
