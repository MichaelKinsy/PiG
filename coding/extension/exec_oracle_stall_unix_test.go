//go:build unix

package extension

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/childwait"
	"github.com/MichaelKinsy/PiG/internal/testbudget"
)

// execStallChildEnv carries the scenario that TestExecStallChild runs.
const execStallChildEnv = "PIG_EXEC_STALL_SCENARIO"

// execStall is how long the caller is kept off the CPU: past the timer of every timer row that can fire, and past the end of the trap row's script on Unix.
const execStall = 5 * time.Second

// TestExecStallChild runs the scenario in execStallChildEnv through ExecCommand, as the child of TestExecOracleTimerScenariosHoldWhileTheCallerStalls,
// and writes the outcome to stdout. Its readers stop the whole process with SIGSTOP before their first read, when the shell runs and the timer is set,
// after reporting "stalled" on stderr, and wait for the SIGCONT that ends the stall: ExecCommand can settle only after it.
func TestExecStallChild(t *testing.T) {
	raw := os.Getenv(execStallChildEnv)
	if raw == "" {
		return
	}
	var scenario execScenario
	if err := json.Unmarshal([]byte(raw), &scenario); err != nil {
		panic(err)
	}
	continued := make(chan os.Signal, 1)
	signal.Notify(continued, syscall.SIGCONT)
	resumed := make(chan struct{})
	go func() {
		<-continued
		close(resumed)
	}()
	var stall sync.Once
	ctx := WithExecDrainHooks(context.Background(), &childwait.Hooks{BeforeRead: func() {
		stall.Do(func() {
			_, _ = os.Stderr.WriteString("stalled\n")
			if err := syscall.Kill(os.Getpid(), syscall.SIGSTOP); err != nil {
				panic(err)
			}
		})
		<-resumed
	}})
	outcome, err := json.Marshal(pigExecOutcome(ctx, scenario))
	if err != nil {
		panic(err)
	}
	_, _ = os.Stdout.Write(outcome)
	os.Exit(0)
}

// A host that keeps the caller off the CPU for seconds lets a short timer expire and a short script finish before ExecCommand runs again, and the call
// then reads what the script wrote meanwhile, killed: with scripts and timers of a second or two, `sleep 2; echo late` and a 1000 ms timer resolved
// "late\n" killed, and `echo quick` with the same timer resolved killed, where Pi on a prompt host resolved no output and not killed (Windows CI, native-4).
// exec.ts appends until the pipes end, not until the kill, so Pi's call reads the same after such a stall, and no PiG code can tell the two runs apart:
// the rows must leave more than a stall between the timer and the script. Each caller is the test binary running one timer row, stopped with SIGSTOP
// once its shell runs and its timer is set and resumed execStall later, while Pi runs the same row on a prompt host. Every outcome must be Pi's.
func TestExecOracleTimerScenariosHoldWhileTheCallerStalls(t *testing.T) {
	scenarios := execTimerScenarios(t.TempDir())

	expected := make([]execOutcome, len(scenarios))
	oracleErrors := make([]error, len(scenarios))
	var oracle sync.WaitGroup
	for i, scenario := range scenarios {
		oracle.Go(func() {
			outcomes, err := runPiExec(t.Context(), []execScenario{scenario})
			if err == nil {
				expected[i] = outcomes[0]
			}
			oracleErrors[i] = err
		})
	}

	type call struct {
		cmd     *exec.Cmd
		stdout  bytes.Buffer
		stalled chan error
	}
	calls := make([]*call, len(scenarios))
	for i, scenario := range scenarios {
		raw, err := json.Marshal(scenario)
		if err != nil {
			t.Fatal(err)
		}
		c := &call{cmd: exec.Command(os.Args[0], "-test.run=^TestExecStallChild$"), stalled: make(chan error, 1)}
		c.cmd.Env = append(os.Environ(), execStallChildEnv+"="+string(raw))
		c.cmd.Stdout = &c.stdout
		stderr, err := c.cmd.StderrPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.cmd.Start(); err != nil {
			t.Fatal(err)
		}
		// A stopped caller must not outlive a failed test.
		t.Cleanup(func() {
			_ = c.cmd.Process.Signal(syscall.SIGCONT)
			_ = c.cmd.Process.Kill()
			_ = c.cmd.Wait()
		})
		go func() {
			line, err := bufio.NewReader(stderr).ReadString('\n')
			switch {
			case err != nil && err != io.EOF:
				c.stalled <- err
			case line != "stalled\n":
				c.stalled <- fmt.Errorf("unexpected first stderr line %q", line)
			default:
				c.stalled <- nil
			}
		}()
		calls[i] = c
	}

	wait := time.After(testbudget.Wait(t))
	for i, c := range calls {
		select {
		case err := <-c.stalled:
			if err != nil {
				t.Fatalf("scenario %d did not stall: %v\nstdout %q", i, err, c.stdout.String())
			}
		case <-wait:
			t.Fatalf("scenario %d did not stall", i)
		}
	}
	time.Sleep(execStall)
	for _, c := range calls {
		if err := c.cmd.Process.Signal(syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
	}
	oracle.Wait()

	for i, c := range calls {
		if oracleErrors[i] != nil {
			t.Fatal(oracleErrors[i])
		}
		if err := c.cmd.Wait(); err != nil {
			t.Fatalf("scenario %d: %v", i, err)
		}
		var got execOutcome
		if err := json.Unmarshal(c.stdout.Bytes(), &got); err != nil {
			t.Fatalf("scenario %d: %v\nstdout %q", i, err, c.stdout.String())
		}
		if !reflect.DeepEqual(got, expected[i]) {
			in, _ := json.Marshal(scenarios[i])
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(expected[i])
			t.Errorf("scenario %d %.300s after a %s stall\n  Pig %.400s\n  Pi  %.400s", i, in, execStall, gotJSON, wantJSON)
		}
	}
}
