package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
)

// bashParams marshals shell tool input in tests; a zero timeout is omitted.
type bashParams struct {
	Command string  `json:"command"`
	Timeout float64 `json:"timeout,omitempty"`
}

// TestBashKillsProcessGroupOnCancel reproduces the "bash hangs forever
// because a grandchild holds the stdout pipe" bug. The script
// background-spawns a `sleep` that inherits stdout and stays alive past
// the bash parent. Without Setpgid+process-group-kill, cmd.Wait blocks
// on the child's pipe and Execute never returns.
//
// With the fix, the entire process group is SIGKILLed, the child dies,
// and Execute returns within a couple of seconds.
func TestBashKillsProcessGroupOnCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping in -short mode")
	}
	bt := &BashTool{CWD: t.TempDir()}

	// Long-running grandchild that would normally hold the pipe open.
	args, _ := json.Marshal(bashParams{Command: "sleep 30 &\necho started\nwait"})

	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan agent.AgentToolResult, 1)
	go func() {
		res, _ := bt.Execute(ctx, "", args, nil)
		resultCh <- res
	}()

	// Give bash time to start.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case res := <-resultCh:
		if !res.IsError {
			t.Errorf("expected IsError after cancel, got success: %s", res.Content)
		}
		if !strings.Contains(res.Content, "aborted") {
			t.Errorf("expected abort message, got: %s", res.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("bash didn't return within 5s after cancel \u2014 process group kill is broken (children still hold pipes)")
	}
}

func TestBashSucceedsWithSimpleCommand(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "echo hello"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := bt.Execute(ctx, "", args, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Errorf("simple echo should succeed, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("missing output: %q", res.Content)
	}
}

// TestBashStreamsLiveOutput simulates a slow producer and verifies the
// onUpdate callback fires with progressive snapshots while bash is
// still running. This is the contract the TUI relies on for live tool
// rendering.
func TestBashStreamsLiveOutput(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	// Print 5 lines, 100ms apart.
	args, _ := json.Marshal(bashParams{Command: "for i in 1 2 3 4 5; do echo line$i; sleep 0.1; done"})

	var (
		mu      sync.Mutex
		updates []string
	)
	onUpdate := func(content string, _ any) {
		mu.Lock()
		updates = append(updates, content)
		mu.Unlock()
	}

	res, err := bt.Execute(context.Background(), "", args, onUpdate)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(updates) < 2 {
		t.Fatalf("expected multiple progressive updates from a 0.5s producer, got %d: %v", len(updates), updates)
	}
	first := updates[0]
	last := updates[len(updates)-1]
	if !strings.HasPrefix(last, first) {
		t.Errorf("snapshots must be cumulative; first=%q not a prefix of last=%q", first, last)
	}
	for i := 1; i <= 5; i++ {
		if !strings.Contains(res.Content, fmt.Sprintf("line%d", i)) {
			t.Errorf("final output missing line%d: %q", i, res.Content)
		}
	}
}

// TestBashFirstUpdateFiresImmediately verifies the upstream-matching
// behavior where the first data chunk fires OnLiveUpdate immediately
// (zero delay) instead of waiting the full 100ms throttle window.
// This is the key difference that makes streaming feel responsive.
// Upstream: lastUpdateAt starts at 0, so delay = 100 - (now - 0) < 0.
func TestBashFirstUpdateFiresImmediately(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	// Echo a single line and exit immediately.
	args, _ := json.Marshal(bashParams{Command: "echo hello"})

	var (
		mu          sync.Mutex
		firstAt     time.Time
		updateCount int
	)
	onUpdate := func(content string, _ any) {
		mu.Lock()
		updateCount++
		if firstAt.IsZero() && content != "" {
			firstAt = time.Now()
		}
		mu.Unlock()
	}

	res, err := bt.Execute(context.Background(), "", args, onUpdate)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}

	mu.Lock()
	defer mu.Unlock()

	if firstAt.IsZero() {
		t.Fatal("never received a non-empty update")
	}

}

func TestBashUpdateDelay(t *testing.T) {
	now := time.Unix(100, 0)
	if delay := bashUpdateDelay(time.Time{}, now); delay != 0 {
		t.Fatalf("first update delay = %v, want 0", delay)
	}
	last := now.Add(-25 * time.Millisecond)
	if delay := bashUpdateDelay(last, now); delay != 75*time.Millisecond {
		t.Fatalf("recent update delay = %v, want 75ms", delay)
	}
	last = now.Add(-bashUpdateThrottle)
	if delay := bashUpdateDelay(last, now); delay != 0 {
		t.Fatalf("elapsed throttle delay = %v, want 0", delay)
	}
}

// TestBashNoDeadlockWithSlowConsumer verifies that a fast-producing
// bash command doesn't deadlock when the OnLiveUpdate callback is
// slow (simulating a full event channel + slow TUI render). Before
// the fix, emitOutputUpdate was called synchronously on the pipe-read
// goroutine, so a blocking callback would stall reads, fill the pipe
// buffer, and hang the bash command.
func TestBashNoDeadlockWithSlowConsumer(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	// seq 10000 produces ~50KB of output very quickly.
	args, _ := json.Marshal(bashParams{Command: "seq 1 10000"})

	var (
		mu      sync.Mutex
		updates int
	)
	// Simulate a slow consumer: each callback takes 200ms.
	// With 64-slot channel + synchronous emit, this would deadlock
	// the old code because the read goroutine would block on emit.
	onUpdate := func(content string, _ any) {
		time.Sleep(200 * time.Millisecond)
		mu.Lock()
		updates++
		mu.Unlock()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := bt.Execute(ctx, "", args, onUpdate)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.IsError {
		// Could be a timeout-caused error; that's OK as long as we didn't deadlock.
		t.Logf("got error (OK if not a timeout): %s", res.Content)
	}
	if ctx.Err() != nil {
		t.Fatal("timed out: deadlock: bash hung because read goroutine was blocked on slow consumer")
	}
	if !strings.Contains(res.Content, "10000") {
		t.Errorf("expected output to contain '10000', got %q", res.Content[:min(200, len(res.Content))])
	}
}

func TestBashTimeoutMessage(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "sleep 5", Timeout: 0.5})
	ctx := t.Context()
	start := time.Now()
	res, _ := bt.Execute(ctx, "", args, nil)
	elapsed := time.Since(start)
	if elapsed > 4*time.Second {
		t.Errorf("timeout=0.5 should return in well under 5s, took %v", elapsed)
	}
	if !res.IsError {
		t.Error("timeout should produce IsError=true")
	}
	if !strings.Contains(res.Content, "timed out") {
		t.Errorf("missing timeout message in: %q", res.Content)
	}
}

// TestBashRejectsOverlargeTimeout guards the duration-overflow path: a timeout
// beyond maxBashTimeoutSeconds must be rejected, not silently wrapped negative
// (which would expire the command immediately). Mirrors upstream resolveTimeoutMs.
func TestBashRejectsOverlargeTimeout(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "echo hi", Timeout: maxBashTimeoutSeconds + 1})
	res, err := bt.Execute(t.Context(), "", args, nil)
	if err != nil {
		t.Fatalf("unexpected hard error: %v", err)
	}
	if !res.IsError {
		t.Fatal("over-large timeout should produce IsError=true")
	}
	if !strings.Contains(res.Content, "Invalid timeout") {
		t.Errorf("missing invalid-timeout message in: %q", res.Content)
	}
}

func TestEditDetailsAttached(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/x.txt"
	if err := os.WriteFile(path, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	et := &EditTool{CWD: dir}
	args, _ := json.Marshal(editParams{
		Path: "x.txt",
		Edits: []editEntry{
			{OldText: "alpha", NewText: "ALPHA"},
			{OldText: "beta", NewText: "BETA"},
		},
	})
	res, err := et.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("edit failed: %v %s", err, res.Content)
	}
	d, ok := res.Details.(*EditToolDetails)
	if !ok || d == nil {
		t.Fatalf("expected EditToolDetails, got %T", res.Details)
	}
	// Two edited lines (alpha→ALPHA, beta→BETA); gamma stays as context.
	for _, want := range []string{"alpha", "ALPHA", "beta", "BETA"} {
		if !strings.Contains(d.Diff, want) {
			t.Errorf("diff missing %q:\n%s", want, d.Diff)
		}
	}
	if !strings.Contains(d.Patch, "@@") || !strings.HasPrefix(d.Patch, "--- ") {
		t.Errorf("patch not a unified diff:\n%s", d.Patch)
	}
	if d.FirstChangedLine != 1 {
		t.Errorf("FirstChangedLine = %d, want 1", d.FirstChangedLine)
	}
}

// ─── parity tests ────────────────────────────────────────────────

// fakeSettings is a SettingsView for tests.
type fakeSettings struct {
	path string
}

func (s fakeSettings) GetShellPath() (string, error) { return s.path, nil }

func TestBashCommandPrefixApplied(t *testing.T) {
	// Prefix sets a variable that the command then echoes: proves
	// the prefix runs in the same shell invocation as the command.
	bt := &BashTool{CWD: t.TempDir(), CommandPrefix: "PREFIX_VAR=set_by_prefix"}
	args, _ := json.Marshal(bashParams{Command: "echo $PREFIX_VAR"})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v IsError=%v content=%s", err, res.IsError, res.Content)
	}
	if !strings.Contains(res.Content, "set_by_prefix") {
		t.Errorf("prefix not applied; content=%q", res.Content)
	}
}

func TestBashShellPathHonored(t *testing.T) {
	// Upstream getShellConfig runs any existing shellPath. /bin/sh exists on
	// Unix; Git for Windows ships sh.exe beside the bash.exe Pi selects there.
	shell := "/bin/sh"
	if runtime.GOOS == "windows" {
		bash, err := defaultShellConfig()
		if err != nil {
			t.Fatal(err)
		}
		shell = filepath.Join(filepath.Dir(bash.Path), "sh.exe")
	}
	bt := &BashTool{
		CWD:      t.TempDir(),
		Settings: fakeSettings{path: shell},
	}
	args, _ := json.Marshal(bashParams{Command: "echo from-sh"})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v content=%s", err, res.Content)
	}
	if !strings.Contains(res.Content, "from-sh") {
		t.Errorf("output: %q", res.Content)
	}
}

// Upstream's bash tool hands the model the decoded output unchanged; only the
// renderer and user bash strip ANSI and control characters.
func TestBashToolOutputIsRaw(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: `printf '\033[31mERR\033[0m\r\x01'`})
	res, err := bt.Execute(context.Background(), "", args, nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: %v %s", err, res.Content)
	}
	if res.Content != "\x1b[31mERR\x1b[0m\r\x01" {
		t.Errorf("tool output = %q, want the raw bytes", res.Content)
	}
}

// User bash (executeBashWithOperations) sanitizes each chunk.
func TestExecuteBashSanitizesOutput(t *testing.T) {
	sh, err := defaultShellConfig()
	if err != nil {
		t.Fatal(err)
	}
	res, err := ExecuteBash(context.Background(), `printf '\033[31mERR\033[0m\r\x01ok'`, t.TempDir(), sh, BashExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(res.Output, "\x1b\r\x01") || !strings.Contains(res.Output, "ERR") || !strings.Contains(res.Output, "ok") {
		t.Errorf("user bash output = %q, want sanitized text", res.Output)
	}
}

func TestBashExitCodeAsError(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "exit 7"})
	res, _ := bt.Execute(context.Background(), "", args, nil)
	if !res.IsError {
		t.Errorf("non-zero exit must be IsError")
	}
	if !strings.Contains(res.Content, "Command exited with code 7") {
		t.Errorf("missing 'Command exited with code 7' message; got %q", res.Content)
	}
}

func TestBashTempFileOverflow(t *testing.T) {
	// Produce 60 KB of output (above DEFAULT_MAX_BYTES = 50 KB) so the
	// tempfile path triggers and a truncation warning is emitted.
	bt := &BashTool{CWD: t.TempDir()}
	// ~3000 lines of "x" each → bytes overflow first.
	args, _ := json.Marshal(bashParams{Command: `awk 'BEGIN{for(i=1;i<=3000;i++) printf "%020d\n", i}'`})
	res, _ := bt.Execute(context.Background(), "", args, nil)

	d, ok := res.Details.(*BashDetails)
	if !ok || d == nil {
		t.Fatalf("expected *BashDetails on truncated output, got %T (%+v)", res.Details, res.Details)
	}
	if d.FullOutputPath == "" {
		t.Errorf("expected full output path to be set")
	}
	// Tempfile should be readable and contain the original full output.
	full, err := os.ReadFile(d.FullOutputPath)
	if err != nil {
		t.Fatalf("read tempfile %s: %v", d.FullOutputPath, err)
	}
	if len(full) < 50_000 {
		t.Errorf("tempfile too small (%d bytes); should hold full untruncated output", len(full))
	}
	if !strings.Contains(res.Content, "[Showing lines") {
		t.Errorf("expected '[Showing lines ...]' annotation in content; got %q", res.Content[:min(200, len(res.Content))])
	}
	if !strings.Contains(res.Content, d.FullOutputPath) {
		t.Errorf("annotation should mention tempfile path %s; content=%q",
			d.FullOutputPath, res.Content[max(0, len(res.Content)-300):])
	}
}

func TestBashAbortReturnsBufferedOutput(t *testing.T) {
	bt := &BashTool{CWD: t.TempDir()}
	args, _ := json.Marshal(bashParams{Command: "echo started\nsleep 30"})
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan agent.AgentToolResult, 1)
	started := make(chan struct{})
	var startedOnce sync.Once
	go func() {
		res, _ := bt.Execute(ctx, "", args, func(content string, _ any) {
			if strings.Contains(content, "started") {
				startedOnce.Do(func() { close(started) })
			}
		})
		resultCh <- res
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("bash command did not produce its pre-abort output")
	}
	cancel()
	select {
	case res := <-resultCh:
		if !res.IsError {
			t.Errorf("expected IsError after abort")
		}
		if !strings.Contains(res.Content, "started") {
			t.Errorf("buffered output 'started' must survive abort; got %q", res.Content)
		}
		if !strings.Contains(res.Content, "Command aborted") {
			t.Errorf("expected 'Command aborted' message; got %q", res.Content)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort didn't return within 5s")
	}
}

func TestBashCwdDoesNotPersistMatchingUpstream(t *testing.T) {
	// Locked decision A on row 2.14: pig matches upstream by spawning
	// one shell per call. cd between invocations must NOT persist.
	bt := &BashTool{CWD: t.TempDir()}

	// Call 1: cd /tmp.
	args1, _ := json.Marshal(bashParams{Command: "cd /tmp && pwd"})
	res1, _ := bt.Execute(context.Background(), "", args1, nil)
	if !strings.Contains(res1.Content, "/tmp") {
		t.Fatalf("first call: cd /tmp should report /tmp; got %q", res1.Content)
	}

	// Call 2: pwd (no cd). Should report the original CWD, NOT /tmp. Git Bash
	// maps %TEMP% (which holds t.TempDir) to /tmp, so on Windows the call asks
	// for the Windows form of the directory with pwd -W.
	pwd, wantCWD := "pwd", bt.CWD
	if runtime.GOOS == "windows" {
		pwd, wantCWD = "pwd -W", filepath.ToSlash(bt.CWD)
	}
	args2, _ := json.Marshal(bashParams{Command: pwd})
	res2, _ := bt.Execute(context.Background(), "", args2, nil)
	if strings.Contains(strings.TrimSpace(res2.Content), "/tmp\n") || strings.TrimSpace(res2.Content) == "/tmp" {
		t.Errorf("second call cwd persisted from first (pig must match upstream's one-shot semantics); got %q", res2.Content)
	}
	if !strings.Contains(res2.Content, wantCWD) {
		t.Errorf("second call should report tool's CWD %q; got %q", wantCWD, res2.Content)
	}
}
