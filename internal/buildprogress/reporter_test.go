package buildprogress

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

func TestReporterGolden(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprintf("tty-%t", tty), func(t *testing.T) {
			var output bytes.Buffer
			now := time.Unix(0, 0)
			r := newReporter(&output, true, tty, func() int { return 80 }, func() time.Time { return now })
			r.Handle(Event{Phase: "Resolving manifest", Step: "small.yaml"})
			now = now.Add(2 * time.Second)
			r.Handle(Event{Phase: "Compiling Go members", Step: "hello"})
			r.frame++
			r.render()
			r.Handle(Event{Member: "hello", Output: "  compiler line\n"})
			now = now.Add(time.Second)
			r.Success("/out/pig-small", 1024)
			before := output.String()
			r.Handle(Event{Phase: "late event"})
			r.Close()
			if output.String() != before {
				t.Fatal("write after close")
			}
			golden := filepath.Join("testdata", fmt.Sprintf("tty-%t.golden", tty))
			expected, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got := strconv.Quote(output.String()) + "\n"; got != string(expected) {
				t.Fatalf("got:\n%s\nwant:\n%s", got, expected)
			}
		})
	}
}

func TestReporterDiagnosticPreservesStatus(t *testing.T) {
	var output bytes.Buffer
	now := time.Unix(0, 0)
	r := newReporter(&output, false, true, func() int { return 80 }, func() time.Time { return now })
	r.Handle(Event{Phase: "Packing", Step: "hello"})
	if _, err := r.Write([]byte("warning: external member\n")); err != nil {
		t.Fatal(err)
	}
	r.Close()
	frame := "⠋ Packing (0s)\n\x1b[2m  hello\x1b[0m"
	clear := "\r\x1b[2K\x1b[1A\r\x1b[2K"
	if want := frame + clear + "warning: external member\n" + frame + clear; output.String() != want {
		t.Fatalf("diagnostic output=%q want=%q", output.String(), want)
	}
	if _, err := r.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late diagnostic: %v", err)
	}
}

func TestReporterClipsTerminalRowsAndSanitizesControls(t *testing.T) {
	var output bytes.Buffer
	r := newReporter(&output, false, true, func() int { return 12 }, time.Now)
	r.Handle(Event{Phase: "Very long phase", Step: "untrusted\x1b[2J\r\npath"})
	for line := range strings.SplitSeq(output.String(), "\n") {
		if ansi.StringWidth(line) > 11 {
			t.Errorf("wrapped row: %q", line)
		}
	}
	if strings.Contains(output.String(), "\x1b[2J") {
		t.Fatal("injected terminal control")
	}
	r.Close()
}

func TestReporterFailureTailPreservesCauseAndPhase(t *testing.T) {
	var output bytes.Buffer
	r := New(&output, false)
	r.Handle(Event{Phase: "Compiling Rust members", Step: "chain-rs"})
	cause := errors.New("first diagnostic\n" + strings.Repeat("x", 32*1024) + "\nlast diagnostic")
	err := r.Failure(cause)
	for _, want := range []string{"Compiling Rust members failed", "chain-rs", "last diagnostic", "hint:", "truncated"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(err.Error(), "first diagnostic") {
		t.Fatal("did not bound diagnostic tail")
	}
	if !errors.Is(err, cause) {
		t.Fatal("lost structured error cause")
	}
}

func TestReporterJoinsAnimationAndConcurrentWriters(t *testing.T) {
	var output bytes.Buffer
	r := newReporter(&output, true, true, func() int { return 80 }, time.Now)
	r.stop, r.done = make(chan struct{}), make(chan struct{})
	go r.animate()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				r.Handle(Event{Phase: "Compiling", Step: "hello"})
				r.Handle(Event{Member: "hello", Output: "line\n"})
			}
		})
	}
	wg.Wait()
	for range 8 {
		wg.Go(r.Close)
	}
	wg.Wait()
	select {
	case <-r.done:
	default:
		t.Fatal("animation still running")
	}
	before := output.String()
	r.Handle(Event{Member: "hello", Output: "late\n"})
	if output.String() != before {
		t.Fatal("late write")
	}
}

func TestPhaseEventsAndLiveToolOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var events []Event
	live := make(chan struct{})
	ctx = Observe(ctx, func(event Event) {
		events = append(events, event)
		if event.Output == "compiler started\n" {
			close(live)
		}
	}, true)
	ctx = Member(ctx, "member-a, member-b")
	Phase(ctx, "Compiling Go members", "member-a, member-b")
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProgressToolHelper$")
	cmd.Env = append(os.Environ(), "PIG_PROGRESS_TOOL_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		output []byte
		err    error
	}
	done := make(chan result, 1)
	go func() { output, err := CombinedOutput(ctx, cmd); done <- result{output, err} }()
	select {
	case <-live:
	case <-ctx.Done():
		t.Fatal("tool output was buffered until completion")
	}
	if _, err := io.WriteString(stdin, "continue\n"); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()
	got := <-done
	if got.err == nil || string(got.output) != "compiler started\ncompiler failed" {
		t.Fatalf("output=%q err=%v", got.output, got.err)
	}
	want := []Event{
		{Phase: "Compiling Go members", Step: "member-a, member-b"},
		{Member: "member-a, member-b", Output: "compiler started\n"},
		{Member: "member-a, member-b", Output: "compiler failed\n"},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%#v", events)
	}
}

func TestProgressToolHelper(t *testing.T) {
	if os.Getenv("PIG_PROGRESS_TOOL_HELPER") == "volume" {
		_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", diagnosticTailBytes*2)+"\nlast diagnostic")
		os.Exit(3)
	}
	if os.Getenv("PIG_PROGRESS_TOOL_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, "compiler started")
	var b [1]byte
	if _, err := os.Stdin.Read(b[:]); err != nil {
		os.Exit(2)
	}
	_, _ = fmt.Fprint(os.Stderr, "compiler failed")
	os.Exit(3)
}

func TestToolLinesBoundedAndHandlesChunks(t *testing.T) {
	var lines []string
	w := &toolLines{emit: func(s string) { lines = append(lines, s) }}
	input := "first\n" + strings.Repeat("x", maxLine*3+1)
	for _, chunk := range []string{input[:2], input[2:5], input[5:]} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatalf("write=%d %v", n, err)
		}
		if len(w.pending) >= maxLine {
			t.Fatal("unbounded line buffer")
		}
	}
	w.flush()
	if got := strings.Join(lines, ""); got != "first\n"+strings.Repeat(strings.Repeat("x", maxLine)+"\n", 3)+"x\n" {
		t.Fatal("lost tool output")
	}
}

func TestToolVerbosityDoesNotChangeBuildInputs(t *testing.T) {
	quiet := context.Background()
	verbose := Observe(quiet, func(Event) {}, true)
	for _, tc := range []struct {
		language   string
		args, want []string
	}{
		{"go", []string{"build", "-o", "out", "."}, []string{"build", "-v", "-o", "out", "."}},
		{"rust", []string{"build", "--release", "--quiet"}, []string{"build", "--release"}},
	} {
		original := strings.Join(tc.args, " ")
		if got := ToolArgs(verbose, tc.language, tc.args); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("got=%v want=%v", got, tc.want)
		}
		if got := strings.Join(ToolArgs(quiet, tc.language, tc.args), " "); got != original {
			t.Fatal("changed non-build caller")
		}
	}
}

func TestRunBoundsCompilerDiagnostics(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProgressToolHelper$")
	cmd.Env = append(os.Environ(), "PIG_PROGRESS_TOOL_HELPER=volume")
	err := Run(context.Background(), cmd)
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Fatalf("exit=%v", err)
	}
	if len(err.Error()) > diagnosticTailBytes+len("exit status 3\n") || !strings.HasSuffix(err.Error(), "last diagnostic") {
		t.Fatalf("bad diagnostic tail: %d bytes", len(err.Error()))
	}
}

func TestToolLinesPreservesUTF8AtChunkBoundary(t *testing.T) {
	var output strings.Builder
	w := &toolLines{emit: func(s string) {
		if !utf8.ValidString(s) {
			t.Fatalf("split UTF-8: %q", s)
		}
		output.WriteString(strings.TrimSuffix(s, "\n"))
	}}
	input := strings.Repeat("界", maxLine)
	for i := range len(input) {
		_, _ = w.Write([]byte{input[i]})
	}
	w.flush()
	if output.String() != input {
		t.Fatal("lost toolchain output")
	}
}

func TestReporterRetainsBoundedToolTail(t *testing.T) {
	r := New(io.Discard, false)
	r.Handle(Event{Phase: "Compiling", Step: "member"})
	var expected strings.Builder
	for range 1000 {
		line := "[member] " + strings.Repeat("界", 32) + "\n"
		expected.WriteString(line)
		r.Handle(Event{Member: "member", Output: strings.Repeat("界", 32) + "\n"})
	}
	want := expected.String()
	want = want[len(want)-diagnosticTailBytes:]
	if string(r.tail) != want || cap(r.tail) != diagnosticTailBytes {
		t.Fatalf("tail=%d bytes, capacity=%d", len(r.tail), cap(r.tail))
	}
	err := r.Failure(errors.New("compiler exit\nunrelated appended metadata"))
	if !utf8.ValidString(err.Error()) || strings.Contains(err.Error(), "unrelated appended metadata") {
		t.Fatalf("diagnostic: %s", err)
	}
}

func TestToolOutputCancellationJoinsProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = Observe(ctx, func(event Event) {
		if event.Output == "compiler started\n" {
			cancel()
		}
	}, false)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProgressToolHelper$")
	cmd.Env = append(os.Environ(), "PIG_PROGRESS_TOOL_HELPER=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	output, err := CombinedOutput(ctx, cmd)
	if err == nil || ctx.Err() != context.Canceled || cmd.ProcessState == nil || string(output) != "compiler started\n" {
		t.Fatalf("output=%q err=%v context=%v", output, err, ctx.Err())
	}
}

func BenchmarkBuildProgressOutput(b *testing.B) {
	r := New(io.Discard, true)
	defer r.Close()
	b.ReportAllocs()
	for b.Loop() {
		r.Handle(Event{Member: "chain-rs", Output: "   Compiling pig-sdk\n"})
	}
}
