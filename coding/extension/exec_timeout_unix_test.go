//go:build unix

package extension

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/coding/pigversion"
	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// execTimerScript is testdata/exec_timer.mjs, which a test that changes its working directory still finds.
var execTimerScript, _ = filepath.Abs("testdata/exec_timer.mjs")

// exec.ts:75 starts the timeout once spawn has returned, so the time spawn takes to find and start the program does not
// count against it. A PATH of many directories that each cost a long symlink walk makes finding the program take seconds,
// in Node's spawn (libuv searches the PATH before spawn returns) and in PiG; a timeout of one second must then end the
// program a second after spawn returned, not while spawn was still finding it. The program outlives the timeout many
// times over, so the timer ends it, and the test reads when spawn returned and how long after that the call resolved: a
// lower bound that a loaded host can only lengthen. A program that had to finish inside the timeout would fail whenever
// the host took a second to start it. Pi's execCommand, with its spawn timed, is the oracle.
func TestExecCommandStartsTheTimeoutAfterTheSpawnAsPiDoes(t *testing.T) {
	dir := t.TempDir()
	// Relative PATH entries resolve in the working directory, which is dir in
	// PiG, in Node, and in the child, and keep the environment small.
	t.Chdir(dir)
	if err := os.Mkdir("d", 0o755); err != nil {
		t.Fatal(err)
	}
	link := "d"
	for i := range 30 {
		name := "l" + strconv.Itoa(i)
		if err := os.Symlink(strings.Repeat("d/../", 200)+link, name); err != nil {
			t.Fatal(err)
		}
		link = name
	}
	timeout := 1000.0
	limit := time.Duration(timeout) * time.Millisecond
	// Each entry is 4 bytes; 30,000 keep the variable under the kernel's 128 KiB limit for one environment string.
	const maxEntries = 30000
	original := os.Getenv("PATH")
	setPath := func(entries int) { t.Setenv("PATH", strings.Repeat(link+":", entries)+original) }
	// The PATH is first sized on this host, now, so that finding sleep takes at least twice the timeout: a fixed 20,000
	// entries took seconds per search on an idle host and minutes on a loaded one, where the package's 10-minute
	// deadline expired. Each run below reports how long its own search took, and the test grows the PATH until it
	// outlasts the timeout in that run.
	entries := 500
	for {
		setPath(entries)
		start := time.Now()
		if _, err := nodespawn.LookPath("sleep", dir, os.Environ()); err != nil {
			t.Fatal(err)
		}
		if searched := time.Since(start); searched >= 2*limit || entries == maxEntries {
			t.Logf("a PATH of %d entries: finding sleep took %v", entries, searched)
			break
		}
		entries = min(entries*2, maxEntries)
	}
	scenario := execScenario{Command: "sleep", Args: []string{"30"}, CWD: dir, Timeout: &timeout}

	// search is how long spawn took to return; afterSpawn is how long the call took to resolve from then.
	type timeline struct {
		outcome            execOutcome
		search, afterSpawn time.Duration
	}
	runPi := func() timeline {
		input, err := json.Marshal(scenario)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), execOracleNode, execTimerScript, pigversion.UpstreamPackageVersion)
		cmd.Stdin = bytes.NewReader(input)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("Pi oracle: %v\n%s", err, &stderr)
		}
		var report struct {
			Stdout       string  `json:"stdout"`
			Stderr       string  `json:"stderr"`
			Code         int     `json:"code"`
			Killed       bool    `json:"killed"`
			SearchMs     float64 `json:"searchMs"`
			AfterSpawnMs float64 `json:"afterSpawnMs"`
		}
		if err := json.Unmarshal(output, &report); err != nil {
			t.Fatal(err)
		}
		return timeline{
			outcome:    execOutcome{Stdout: report.Stdout, Stderr: report.Stderr, Code: report.Code, Killed: report.Killed},
			search:     time.Duration(report.SearchMs * float64(time.Millisecond)),
			afterSpawn: time.Duration(report.AfterSpawnMs * float64(time.Millisecond)),
		}
	}
	runPiG := func() timeline {
		// ExecCommand reports the end of the spawn to the initiation mark of its context, before it starts the timer.
		var spawned time.Time
		ctx := WithCallInitiation(context.Background(), func() { spawned = time.Now() })
		start := time.Now()
		result, err := ExecCommand(ctx, dir, scenario.Command, scenario.Args, &ExecOptions{Timeout: timeout})
		end := time.Now()
		if err != nil {
			t.Fatal(err)
		}
		if spawned.IsZero() {
			t.Fatal("ExecCommand did not report that its spawn returned")
		}
		return timeline{
			outcome:    execOutcome{Stdout: result.Stdout, Stderr: result.Stderr, Code: result.Code, Killed: result.Killed},
			search:     spawned.Sub(start),
			afterSpawn: end.Sub(spawned),
		}
	}
	type side struct {
		name string
		run  func() timeline
		// early is how much sooner than its delay the side's timer can fire: Node's timers count whole milliseconds.
		early time.Duration
	}
	pending := []side{{"Pi", runPi, time.Millisecond}, {"PiG", runPiG, 0}}
	for len(pending) > 0 {
		setPath(entries)
		var waiting []side
		for _, s := range pending {
			got := s.run()
			// A timeout that started before the search would end the program when the search ended, which is after the timeout
			// when the search outlasts it; a shorter search tells nothing, so the side runs again on a longer PATH.
			if got.search <= limit {
				waiting = append(waiting, s)
				continue
			}
			t.Logf("%s: a PATH of %d entries: spawn took %v, the call resolved %v after it returned", s.name, entries, got.search, got.afterSpawn)
			if want := (execOutcome{Killed: true}); got.outcome != want {
				t.Errorf("%s: %+v, want %+v", s.name, got.outcome, want)
			}
			if got.afterSpawn < limit-s.early {
				t.Errorf("%s: the call resolved %v after spawn returned, under the %v timeout: the timer started before spawn returned", s.name, got.afterSpawn, limit)
			}
		}
		pending = waiting
		if len(pending) > 0 && entries == maxEntries {
			t.Fatalf("a PATH of %d entries: spawn found sleep in under the timeout in %s, so the test does not tell when the timer starts", entries, pending[0].name)
		}
		entries = min(entries*2, maxEntries)
	}
}
