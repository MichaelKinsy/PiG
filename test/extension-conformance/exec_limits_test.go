package extensionconformance

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// TestExecLimitsHelperProcess is the command the rows below run through pi.exec: it writes the bytes of its second argument (hex) to the stream
// its first names ("stdout" or "stderr"), then ends. The stream "stubborn" is stdout, but the process ignores SIGTERM and then waits to be killed.
// Run as an ordinary test it returns at once.
func TestExecLimitsHelperProcess(t *testing.T) {
	if flag.NArg() != 2 {
		return
	}
	data, err := hex.DecodeString(flag.Arg(1))
	if err != nil {
		t.Fatal(err)
	}
	out := os.Stdout
	if flag.Arg(0) == "stderr" {
		out = os.Stderr
	}
	if flag.Arg(0) == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
		_, _ = out.Write(data)
		time.Sleep(time.Minute)
	}
	_, _ = out.Write(data)
	// The test binary would print PASS on stdout, which the byte caps count.
	os.Exit(0)
}

func execLimitsProbe(t *testing.T, stream, text string, maxBuffer *float64) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	probe := map[string]any{
		"command": exe,
		"args":    []string{"-test.run=^TestExecLimitsHelperProcess$", "--", stream, hex.EncodeToString([]byte(text))},
	}
	if maxBuffer != nil {
		probe["maxBuffer"] = *maxBuffer
	}
	encoded, err := json.Marshal(probe)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

var execLimitsCode = regexp.MustCompile(`code=(\d+);`)

// execLimitsTruncatedCodes are the codes a truncated row may report: the helper that ends by itself gives 1 (Pi's nonzero code for a truncated command that
// exited 0) or 143 when the SIGTERM that ends a truncated command lands before its exit (Pi a83b75a07: 128 + the signal), and the one that ignores SIGTERM
// gives 137, the SIGKILL that follows five seconds later; neither is ever 141 or the other's code. Measured through 11 SDK harness cases (isolated and packed), 200 runs per
// row: the three truncating rows gave 1 x6477 and 143 x123 (PiG ends the child with SIGTERM before it closes the pipes, exec.go), the stubborn row 137 x2200; pi's exec.js on the same
// helper gave 1 x4163 of 4200 loaded runs (the rest lost their output to the idle grace and resolved untruncated), 143 and 141 never, and 137 x1200 of 1200 on the stubborn row.
func execLimitsTruncatedCodes(stream string) []string {
	if stream == "stubborn" {
		return []string{"137"}
	}
	return []string{"1", "143"}
}

// Pi's pi.exec(command, args, { maxBuffer }) caps stdout and stderr together (core/exec.ts, c466a359): output over the cap ends the command,
// the character the cap cuts is dropped whole, and the result carries truncated, killed and a nonzero code. An invalid cap rejects the
// call before anything runs. Every SDK must send the option and read `truncated`: an SDK that drops either resolves the full text with
// truncated=false, or resolves where Pi rejects.
func TestExecLimitsAcrossSDKs(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	rows := []struct {
		name      string
		stream    string
		text      string
		maxBuffer *float64
		want      string
		// posixOnly rows need a signal that a child can ignore.
		posixOnly bool
	}{
		{"cap-cuts-a-character-stdout", "stdout", "a€🙂", f(5), "exec-limits:truncated=true;killed=true;code=nonzero;stdout=a€;stderr=", false},
		{"cap-cuts-a-character-stderr", "stderr", "a€🙂", f(5), "exec-limits:truncated=true;killed=true;code=nonzero;stdout=;stderr=a€", false},
		{"output-within-the-cap", "stdout", "abc", f(3), "exec-limits:truncated=false;killed=false;code=0;stdout=abc;stderr=", false},
		{"zero-cap", "stdout", "x", f(0), "exec-limits:truncated=true;killed=true;code=nonzero;stdout=;stderr=", false},
		{"no-cap", "stdout", "a€🙂", nil, "exec-limits:truncated=false;killed=false;code=0;stdout=a€🙂;stderr=", false},
		// Pi's own regression: a command that ignores SIGTERM is killed five seconds after the cap ended it, so the call returns with the capped output.
		{"cap-on-a-child-that-ignores-sigterm", "stubborn", "xx", f(1), "exec-limits:truncated=true;killed=true;code=nonzero;stdout=x;stderr=", true},
		{"negative-cap-rejects", "stdout", "x", f(-1), "rejected:", false},
		{"fractional-cap-rejects", "stdout", "x", f(0.5), "rejected:", false},
		{"cap-above-the-runtime-limit-rejects", "stdout", "x", f(1 << 40), "rejected:", false},
	}
	run := func(t *testing.T, h *harness) {
		if h.bridge == nil {
			t.Fatal("subprocess harness has no bridge")
		}
		cwd := t.TempDir()
		h.bridge.SetHostAction("exec", func(ctx context.Context, command string, args []string, opts *extension.ExecOptions) (extension.ExecResult, error) {
			return extension.ExecCommand(ctx, cwd, command, args, opts)
		})
		command, ok := findCommand(h.runner, "exec-limits-probe")
		if !ok {
			t.Fatal("exec-limits-probe command missing")
		}
		for _, row := range rows {
			if row.posixOnly && runtime.GOOS == "windows" {
				continue
			}
			*h.notify = nil
			done := make(chan error, 1)
			go func() {
				done <- command.Handler(h.runner.DispatchContext(t.Context()), execLimitsProbe(t, row.stream, row.text, row.maxBuffer))
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s: %v", row.name, err)
				}
			case <-time.After(60 * time.Second):
				t.Fatalf("%s: the call did not return", row.name)
			}
			got := ""
			if len(*h.notify) == 1 {
				got = strings.TrimSuffix((*h.notify)[0], ":info")
			}
			// The probes print the code as it is; a truncated row passes with any code of execLimitsTruncatedCodes, which "code=nonzero" stands for.
			if m := execLimitsCode.FindStringSubmatch(got); m != nil && slices.Contains(execLimitsTruncatedCodes(row.stream), m[1]) && strings.Contains(row.want, "code=nonzero;") {
				got = strings.Replace(got, m[0], "code=nonzero;", 1)
			}
			if strings.HasPrefix(row.want, "rejected:") {
				if !strings.HasPrefix(got, "rejected:") || !strings.Contains(got, "maxBuffer must be an integer between 0 and") {
					t.Errorf("%s: outcome = %q, want a rejection naming the maxBuffer range", row.name, *h.notify)
				}
				continue
			}
			if got != row.want {
				t.Errorf("%s: outcome = %q, want %q", row.name, got, row.want)
			}
		}
	}
	cases := sdkHarnessCases()
	for _, language := range []string{"go", "rust", "python", "lisp"} {
		cases = append(cases, harnessCase{name: "packed-" + language, make: func(t *testing.T) *harness { return makePackedUIHarness(t, language) }})
	}
	for _, tc := range cases {
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
			run(t, h)
		})
	}
}
