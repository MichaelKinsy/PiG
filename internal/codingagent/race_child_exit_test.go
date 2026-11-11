package codingagent

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// raceExitStampEnv, when "1", makes this test binary print the time at which it starts to exit, in Unix nanoseconds, and exit, without running any test. TestHelperProcessesExitAtOnceUnderRace uses it to time the exit alone, apart from how long the process took to start.
const raceExitStampEnv = "PIG_TEST_RACE_EXIT_STAMP"

// runRaceExitStamp is the helper that raceExitStampEnv selects. It returns the exit status for os.Exit.
func runRaceExitStamp() int {
	if _, err := fmt.Println(time.Now().UnixNano()); err != nil {
		fmt.Fprintln(os.Stderr, "race exit stamp:", err)
		return 2
	}
	return 0
}

// A binary built with -race sleeps TSan's atexit_sleep_ms before it exits: 1000 ms by default, and never less. A test that starts this test binary as an editor,
// a trash command, a clipboard tool or a crashing child pays it once per start: TestOpenExternalEditorMatchesPi starts it 608 times, which took 445 s of the
// 600 s that go test allows a package by default. A child that exits within half the sleep did not sleep.
const raceExitBudget = 500 * time.Millisecond

func TestHelperProcessesExitAtOnceUnderRace(t *testing.T) {
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), raceExitStampEnv+"=1")
	out, err := cmd.Output()
	exited := time.Now()
	if err != nil {
		t.Fatalf("run the exit-stamp helper: %v", err)
	}
	stamp, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		t.Fatalf("exit-stamp helper printed %q: %v", out, err)
	}
	if latency := exited.Sub(time.Unix(0, stamp)); latency >= raceExitBudget {
		t.Errorf("the helper took %v from starting to exit to being reaped, want under %v: a -race binary sleeps TSan's atexit_sleep_ms (GORACE) before it exits, and every helper process the tests start pays that second", latency, raceExitBudget)
	}
}
