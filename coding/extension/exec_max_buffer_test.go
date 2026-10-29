package extension_test

// pi: packages/coding-agent/test/exec.test.ts (maxBuffer, #11360), exec-runtime-limit.test.ts

import (
	"context"
	"encoding/hex"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

// nodeMaxStringLength is Node 24's buffer.constants.MAX_STRING_LENGTH on a 64-bit build, the limit Pi's execCommand validates maxBuffer against.
const nodeMaxStringLength = 536870888

func init() {
	// hexwrite STREAM HEX: write the bytes HEX names to STREAM (stdout or stderr) in one write.
	treeHelpers["hexwrite"] = func(args []string) {
		data, err := hex.DecodeString(args[1])
		if err != nil {
			panic(err)
		}
		out := os.Stdout
		if args[0] == "stderr" {
			out = os.Stderr
		}
		_, _ = out.Write(data)
	}
	// outerr: "ab" on stdout, then "cd" on stderr.
	treeHelpers["outerr"] = func([]string) {
		_, _ = os.Stdout.WriteString("ab")
		_, _ = os.Stderr.WriteString("cd")
	}
	// flood: 600 bytes of "e" on stderr, 600 of "o" on stdout, then 1000 bytes of "x" on stdout every 10 ms until the child is ended.
	treeHelpers["flood"] = func([]string) {
		_, _ = os.Stderr.WriteString(strings.Repeat("e", 600))
		_, _ = os.Stdout.WriteString(strings.Repeat("o", 600))
		for deadline := time.Now().Add(holderLifetime); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			_, _ = os.Stdout.WriteString(strings.Repeat("x", 1000))
		}
	}
}

func maxBuffer(n float64) *extension.ExecOptions {
	return &extension.ExecOptions{MaxBuffer: &n}
}

func execHelper(t *testing.T, opts *extension.ExecOptions, mode string, args ...string) (extension.ExecResult, error) {
	t.Helper()
	command, cmdArgs := helper(t, mode, args...)
	return extension.ExecCommand(context.Background(), t.TempDir(), command, cmdArgs, opts)
}

// exec.test.ts "reports spawn errors in stderr" and "keeps stderr empty when a command exits silently": truncated is false.
func TestExecCommandResultsAreNotTruncatedWithoutACap(t *testing.T) {
	missing, err := extension.ExecCommand(context.Background(), t.TempDir(), "pi-command-that-does-not-exist-11085", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Code != 1 || missing.Killed || missing.Truncated || missing.Stdout != "" || !strings.Contains(missing.Stderr, "ENOENT") {
		t.Fatalf("spawn error result = %+v", missing)
	}
	silent, err := execHelper(t, nil, "exit", "1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (extension.ExecResult{Code: 1}); silent != want {
		t.Fatalf("result = %+v, want %+v", silent, want)
	}
}

// exec.test.ts "rejects invalid maxBuffer": the call rejects with a RangeError before it runs anything.
func TestExecCommandRejectsAnInvalidMaxBuffer(t *testing.T) {
	for _, value := range []float64{-1, 0.5, math.NaN(), math.Inf(1), math.Inf(-1), nodeMaxStringLength + 1} {
		_, err := extension.ExecCommand(context.Background(), t.TempDir(), "pi-command-that-does-not-exist-11360", nil, maxBuffer(value))
		if err == nil || !strings.Contains(err.Error(), "maxBuffer must be an integer between 0 and 536870888 bytes") {
			t.Errorf("maxBuffer %v: err = %v, want the RangeError message", value, err)
		}
	}
}

// exec.test.ts "accepts maxBuffer boundary".
func TestExecCommandAcceptsTheMaxBufferBoundaries(t *testing.T) {
	for _, value := range []float64{0, nodeMaxStringLength} {
		result, err := execHelper(t, maxBuffer(value), "exit", "0")
		if err != nil {
			t.Fatal(err)
		}
		if result != (extension.ExecResult{}) {
			t.Errorf("maxBuffer %v: result = %+v, want the zero result", value, result)
		}
	}
}

// exec.test.ts "truncates all output when maxBuffer is zero".
func TestExecCommandTruncatesAllOutputWhenMaxBufferIsZero(t *testing.T) {
	result, err := execHelper(t, maxBuffer(0), "hexwrite", "stdout", "78")
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "" || result.Stderr != "" || !result.Truncated || !result.Killed || result.Code == 0 {
		t.Fatalf("result = %+v, want no output, truncated, killed and a nonzero code", result)
	}
}

// exec.test.ts "caps combined output at maxBuffer, terminates the command, and reports truncation".
func TestExecCommandCapsCombinedOutputAtMaxBuffer(t *testing.T) {
	result, err := execHelper(t, maxBuffer(1000), "flood")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || !result.Killed || result.Code == 0 {
		t.Fatalf("result truncated=%v killed=%v code=%d, want truncated, killed and a nonzero code", result.Truncated, result.Killed, result.Code)
	}
	if got := len(result.Stdout) + len(result.Stderr); got != 1000 {
		t.Fatalf("stdout+stderr = %d bytes, want 1000", got)
	}
}

// exec.test.ts "keeps complete UTF-8 characters from %s output %s capped at %i bytes" (#11360).
func TestExecCommandKeepsCompleteUTF8CharactersUnderTheCap(t *testing.T) {
	for _, tc := range []struct {
		stream, output string
		cap            float64
		want           string
	}{
		{"stdout", "€", 1, ""},
		{"stderr", "€", 1, ""},
		{"stdout", "a€🙂", 5, "a€"},
		{"stderr", "a€🙂", 5, "a€"},
	} {
		result, err := execHelper(t, maxBuffer(tc.cap), "hexwrite", tc.stream, hex.EncodeToString([]byte(tc.output)))
		if err != nil {
			t.Fatal(err)
		}
		got, other := result.Stdout, result.Stderr
		if tc.stream == "stderr" {
			got, other = other, got
		}
		if got != tc.want || other != "" || !result.Truncated || !result.Killed || result.Code == 0 {
			t.Errorf("%s %q cap %v: result = %+v, want %s %q, truncated, killed, nonzero code", tc.stream, tc.output, tc.cap, result, tc.stream, tc.want)
		}
		if len(result.Stdout)+len(result.Stderr) > int(tc.cap) {
			t.Errorf("%s %q cap %v: kept %d bytes", tc.stream, tc.output, tc.cap, len(result.Stdout)+len(result.Stderr))
		}
	}
}

// exec.test.ts "still flushes incomplete UTF-8 at normal EOF when output was not truncated": a decoder flush is one U+FFFD per stream.
func TestExecCommandFlushesIncompleteUTF8AtEOFWithoutATruncation(t *testing.T) {
	command, args := helper(t, "split-incomplete")
	result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (extension.ExecResult{Stdout: "�", Stderr: "�"}); result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
}

// exec.test.ts "leaves output up to maxBuffer unchanged".
func TestExecCommandLeavesOutputUpToMaxBufferUnchanged(t *testing.T) {
	result, err := execHelper(t, maxBuffer(1000), "hexwrite", "stdout", hex.EncodeToString([]byte(strings.Repeat("a", 1000))))
	if err != nil {
		t.Fatal(err)
	}
	if want := (extension.ExecResult{Stdout: strings.Repeat("a", 1000)}); result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
}

// exec-runtime-limit.test.ts: the default cap is 256 MiB capped by the runtime string limit, so a runtime whose limit is below 256 MiB still runs commands, and an
// explicit cap above the runtime limit still rejects.
func TestExecCommandDefaultCapFollowsTheRuntimeStringLimit(t *testing.T) {
	restore := extension.SetExecMaxStringLength(268_435_440)
	defer restore()
	result, err := execHelper(t, nil, "hexwrite", "stdout", hex.EncodeToString([]byte("ok")))
	if err != nil {
		t.Fatal(err)
	}
	if want := (extension.ExecResult{Stdout: "ok"}); result != want {
		t.Fatalf("result = %+v, want %+v", result, want)
	}
	if _, err := execHelper(t, maxBuffer(268_435_440+1), "exit", "0"); err == nil || !strings.Contains(err.Error(), "between 0 and 268435440 bytes") {
		t.Fatalf("err = %v, want the RangeError message for the runtime limit", err)
	}
	if extension.ExecDefaultMaxBuffer() != 268_435_440 {
		t.Fatalf("default cap = %d, want the runtime limit 268435440", extension.ExecDefaultMaxBuffer())
	}
}

func TestExecCommandDefaultCapIs256MiB(t *testing.T) {
	if got := extension.ExecDefaultMaxBuffer(); got != 256*1024*1024 {
		t.Fatalf("default cap = %d, want %d", got, 256*1024*1024)
	}
}

// Node's loop hands the bytes of a child that writes stdout and then stderr to the byte cap in that order (Pi's oracle gave the same
// result in 20 of 20 runs), so a cap of 2 keeps "ab" of stdout and none of stderr every time. Reader goroutines that the scheduler orders freely would
// sometimes keep the stderr bytes first.
func TestExecCommandCountsStdoutBeforeStderrWhenTheChildWroteItFirst(t *testing.T) {
	command, args := helper(t, "outerr")
	for i := range 300 {
		result, err := extension.ExecCommand(context.Background(), t.TempDir(), command, args, maxBuffer(2))
		if err != nil {
			t.Fatal(err)
		}
		if result.Stdout != "ab" || result.Stderr != "" || !result.Truncated {
			t.Fatalf("run %d: result = %+v, want stdout %q, no stderr, truncated", i, result, "ab")
		}
	}
}
