//go:build unix

package extension

import (
	"context"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/nodespawn"
)

// exec.ts:75 starts the timeout once spawn has returned, so the time spawn
// takes to find and start the program does not count against it. A PATH of
// many directories that each cost a long symlink walk makes finding echo take
// seconds, in Node's spawn (libuv searches the PATH before spawn returns) and
// in PiG; the 1-second timeout must then still leave echo the time to write
// and exit. The program is echo, not sh: bash, started as "sh", searches the
// PATH for itself to set $BASH, after spawn has returned. Pi's execCommand is
// the oracle.
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
	// The PATH is sized on this host, now, so that finding echo takes at least twice the timeout. A fixed 20,000 entries
	// took seconds per search on an idle host and minutes on a loaded one, where the package's 10-minute deadline expired.
	// Each entry is 4 bytes; 30,000 keep the variable under the kernel's 128 KiB limit for one environment string.
	original := os.Getenv("PATH")
	for entries := 500; ; entries *= 2 {
		entries = min(entries, 30000)
		t.Setenv("PATH", strings.Repeat(link+":", entries)+original)
		start := time.Now()
		if _, err := nodespawn.LookPath("echo", dir, os.Environ()); err != nil {
			t.Fatal(err)
		}
		if searched := time.Since(start); searched >= 2*time.Duration(timeout)*time.Millisecond || entries == 30000 {
			t.Logf("a PATH of %d entries: finding echo took %v", entries, searched)
			break
		}
	}
	scenario := execScenario{Command: "echo", Args: []string{"quick"}, CWD: dir, Timeout: &timeout}

	want := execOutcome{Stdout: "quick\n"}
	if pi := piExecOutcomes(t, []execScenario{scenario})[0]; pi != want {
		t.Fatalf("Pi: %+v, want %+v", pi, want)
	}
	start := time.Now()
	result, err := ExecCommand(context.Background(), dir, scenario.Command, scenario.Args, &ExecOptions{Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < time.Duration(timeout)*time.Millisecond {
		t.Fatalf("finding echo took %v, under the timeout, so the test does not tell when the timer starts", elapsed)
	}
	if got := (execOutcome{Stdout: result.Stdout, Stderr: result.Stderr, Code: result.Code, Killed: result.Killed}); !reflect.DeepEqual(got, want) {
		t.Errorf("ExecCommand: %+v, want Pi's %+v", got, want)
	}
}
