package codingagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/procpolicy"
)

// Each test here runs itself again as a child test binary without GOMAXPROCS in its environment, so procpolicy.Apply caps GOMAXPROCS as
// Main does (D174). The child prints one line of integers after the test's prefix; the first is the runtime default, the second the
// capped GOMAXPROCS.

const (
	// parallelOverWidthEnv makes the child run parallelOver over 64 parts' worth of items and print the default, the cap, the parts run,
	// the least GOMAXPROCS a part saw and the GOMAXPROCS after.
	parallelOverWidthEnv = "PIG_TEST_PARALLEL_OVER_WIDTH"
	// sessionListingWidthEnv makes the child list a directory of 64 sessions and print the default, the cap, the progress reports, the
	// least GOMAXPROCS a report saw while the workers ran and the GOMAXPROCS after.
	sessionListingWidthEnv = "PIG_TEST_SESSION_LISTING_WIDTH"
)

// cappedChild runs test in a child with env=1 and returns the integers it printed after prefix. It skips when the runtime default is at
// most the cap, where there is nothing to widen.
func cappedChild(t *testing.T, test, env, prefix string) []int {
	t.Helper()
	if testing.Short() {
		t.Skip("applies the GOMAXPROCS policy in a child process")
	}
	command := exec.Command(os.Args[0], "-test.run=^"+test+"$")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GOMAXPROCS=") {
			command.Env = append(command.Env, kv)
		}
	}
	command.Env = append(command.Env, env+"=1")
	out, err := command.Output()
	if err != nil {
		t.Fatalf("child: %v\n%s", err, out)
	}
	for line := range strings.Lines(string(out)) {
		rest, ok := strings.CutPrefix(line, prefix+" ")
		if !ok {
			continue
		}
		var values []int
		for field := range strings.FieldsSeq(rest) {
			value, err := strconv.Atoi(field)
			if err != nil {
				t.Fatalf("child line %q: %v", line, err)
			}
			values = append(values, value)
		}
		if len(values) < 2 {
			t.Fatalf("child line %q: want the default and the cap first", line)
		}
		if values[0] <= procpolicy.Cap {
			t.Skipf("the runtime default is %d Ps here, at most the cap %d: nothing to widen", values[0], procpolicy.Cap)
		}
		if values[1] != procpolicy.Cap {
			t.Fatalf("child capped GOMAXPROCS at %d, want %d", values[1], procpolicy.Cap)
		}
		return values
	}
	t.Fatalf("child printed no %q line:\n%s", prefix, out)
	return nil
}

// D174: a session loader under the GOMAXPROCS cap splits its work by the runtime default width and runs it at that width, and the cap is
// back when it returns. parallelOver is the split of the tree facts, the search texts and every whole-history read (readSlotsLocked).
func TestParallelOverRunsAtTheDefaultWidthUnderTheCap(t *testing.T) {
	if os.Getenv(parallelOverWidthEnv) == "1" {
		defaultProcs := runtime.GOMAXPROCS(0)
		procpolicy.Apply()
		capped := runtime.GOMAXPROCS(0)
		var mu sync.Mutex
		parts, least := 0, defaultProcs+1
		parallelOver(make([]int, 64*parallelReadMinLines), func([]int) {
			procs := runtime.GOMAXPROCS(0)
			mu.Lock()
			parts, least = parts+1, min(least, procs)
			mu.Unlock()
		})
		fmt.Printf("\nparallel-over %d %d %d %d %d\n", defaultProcs, capped, parts, least, runtime.GOMAXPROCS(0))
		return
	}
	got := cappedChild(t, "TestParallelOverRunsAtTheDefaultWidthUnderTheCap", parallelOverWidthEnv, "parallel-over")
	defaultProcs, parts, least, after := got[0], got[2], got[3], got[4]
	if parts != min(defaultProcs, 64) || least != defaultProcs || after != procpolicy.Cap {
		t.Fatalf("default %d: ran %d parts at no less than %d Ps, %d Ps after; want %d parts at %d, %d after",
			defaultProcs, parts, least, after, min(defaultProcs, 64), defaultProcs, procpolicy.Cap)
	}
}

// D174: the session picker's summary workers (Pi's ten, maxConcurrentSessionInfoLoads) run at the runtime default width under the cap,
// and the cap is back when the listing returns. A progress report runs on the listing's own goroutine while workers still summarize the
// remaining files, so the width it reads is the width they run at. At 623c4e3f92 the listing ran on the 2 capped Ps (a cold picker
// over 200 sessions took 566 ms against 328 ms uncapped).
func TestSessionListingRunsAtTheDefaultWidthUnderTheCap(t *testing.T) {
	const sessions = 64
	if os.Getenv(sessionListingWidthEnv) == "1" {
		dir := t.TempDir()
		for i := range sessions {
			header := fmt.Sprintf(`{"type":"session","version":3,"id":"width-%d","timestamp":"2026-01-01T00:00:00.000Z","cwd":"/tmp"}`+"\n", i)
			if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("width-%02d.jsonl", i)), []byte(header), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		defaultProcs := runtime.GOMAXPROCS(0)
		procpolicy.Apply()
		capped := runtime.GOMAXPROCS(0)
		reports, least := 0, defaultProcs+1
		if _, err := listSessionsInDirWithOptions(dir, SessionListOptions{Context: context.Background(), OnProgress: func(int, int, []SessionInfo) {
			reports, least = reports+1, min(least, runtime.GOMAXPROCS(0))
		}}); err != nil {
			t.Fatal(err)
		}
		fmt.Printf("\nsession-listing %d %d %d %d %d\n", defaultProcs, capped, reports, least, runtime.GOMAXPROCS(0))
		return
	}
	got := cappedChild(t, "TestSessionListingRunsAtTheDefaultWidthUnderTheCap", sessionListingWidthEnv, "session-listing")
	defaultProcs, reports, least, after := got[0], got[2], got[3], got[4]
	if reports != sessions || least != defaultProcs || after != procpolicy.Cap {
		t.Fatalf("default %d: %d progress reports at no less than %d Ps, %d Ps after; want %d reports at %d, %d after",
			defaultProcs, reports, least, after, sessions, defaultProcs, procpolicy.Cap)
	}
}
