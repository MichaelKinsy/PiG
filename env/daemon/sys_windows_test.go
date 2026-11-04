//go:build windows

package daemon

// pi: packages/env/daemon/src/sys/windows.rs

// Ports quotes_arguments_like_libuv (windows.rs:648-664). Pi asserts quote_argument on seven vectors; the Go daemon has
// no such function, because its commands start through durable/env/node, whose spawn path builds the command line with
// nodespawn.SetCommandLine. So each vector goes through that real path: the daemon's exec of an argv, whose child (this
// test binary, run as a helper) reports the command line CreateProcessW gave it (GetCommandLineW) and the argv the
// Microsoft C runtime rules split it into (os.Args). Quoting must preserve the arguments, and where Pi asserts the
// exact quoted text, the command line must carry that text.

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

const argvHelper = "PIG_DAEMON_ARGV_HELPER"

// TestMain lets the test binary act as the child that reports how it was started.
func TestMain(m *testing.M) {
	if os.Getenv(argvHelper) == "1" {
		report := struct {
			Args []string `json:"args"`
			Line string   `json:"line"`
		}{os.Args[1:], windows.UTF16PtrToString(syscall.GetCommandLine())}
		if json.NewEncoder(os.Stdout).Encode(report) != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// quoteVectors are the arguments of Pi's test with the text quote_argument returns for each.
var quoteVectors = []struct{ argument, quoted string }{
	{"", `""`},
	{"plain", "plain"},
	{"a b", `"a b"`},
	{"*.txt", "*.txt"},
	{`say "hi"`, `"say \"hi\""`},
	{`C:\dir with space\`, `"C:\dir with space\\"`},
	{`a\\b c`, `"a\\b c"`},
}

// beyondPi is a vector Pi's test lacks, where libuv's rule and Go's syscall.EscapeArg part: libuv quotes an argument
// with a double quote and no space, EscapeArg leaves it bare. It pins that the spawn path applies libuv's rule.
var beyondPi = struct{ argument, quoted string }{`a"b`, `"a\"b"`}

// childReport runs the helper with args through the daemon's exec and returns what the child reported.
func childReport(t *testing.T, p *peer, id uint32, args []string) (received []string, line string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []any{exe}
	for _, argument := range args {
		argv = append(argv, argument)
	}
	p.request(id, Object{"op": "exec", "cwd": t.TempDir(), "argv": argv, "env": Object{argvHelper: "1"}}, nil)
	var stdout strings.Builder
	for {
		frame := p.next()
		if frame.ID != id {
			continue
		}
		switch frame.Kind {
		case FrameEvent:
			if event, ok := frame.JSON.(Object); ok && event["kind"] == "output" && event["stream"] == "stdout" {
				stdout.Write(frame.Payload)
			}
		case FrameResult:
			if code := object(t, frame)["exitCode"]; code != float64(0) {
				t.Fatalf("the helper exited with %v; output %q", code, stdout.String())
			}
			var report struct {
				Args []string `json:"args"`
				Line string   `json:"line"`
			}
			if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
				t.Fatalf("decode %q: %v", stdout.String(), err)
			}
			return report.Args, report.Line
		case FrameError:
			t.Fatalf("exec failed: %v", frame.JSON)
		}
	}
}

func TestExecDeliversPiQuoteArgumentVectorsThroughTheSpawnPath(t *testing.T) {
	p := startServe(t, Options{})
	id := uint32(1)
	check := func(args, quoted []string) {
		t.Helper()
		received, line := childReport(t, p, id, args)
		id++
		if !slices.Equal(received, args) {
			t.Errorf("the child received %q, want %q", received, args)
		}
		// argv[0] is the helper's path, quoted by the same rules; the arguments follow it.
		if want := " " + strings.Join(quoted, " "); !strings.HasSuffix(line, want) {
			t.Errorf("the command line %q does not end with %q", line, want)
		}
	}
	vectors := append(slices.Clone(quoteVectors), beyondPi)
	for _, vector := range vectors {
		check([]string{vector.argument}, []string{vector.quoted})
	}
	// All vectors in one command line: no argument may merge with, split from, or swallow its neighbours.
	var args, quoted []string
	for _, vector := range vectors {
		args = append(args, vector.argument)
		quoted = append(quoted, vector.quoted)
	}
	check(args, quoted)
}

// sys/windows.rs on_exit_signal does nothing, since the daemon cannot catch TerminateProcess: no signal is ever delivered, and
// stopping the delivery is harmless (a87b742f documents that a job object ends each command's own process only).
func TestWindowsDaemonHandlesNoExitSignal(t *testing.T) {
	signals, stop := notifyExitSignals()
	defer stop()
	select {
	case number := <-signals:
		t.Fatalf("received signal %d", number)
	default:
	}
}
