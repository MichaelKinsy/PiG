package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runPrintPromptEndFixture runs pig --print prompt with the Node extension fixture, an extensions-runtime parity fixture that appends its record to the file named by logEnv, and returns that record.
func runPrintPromptEndFixture(t *testing.T, fixture, logEnv, prompt string, extra ...string) string {
	t.Helper()
	entry, err := filepath.Abs(filepath.Join("..", "..", "test", "parity", "scenarios", "extensions-runtime", "testdata", "ext", fixture))
	if err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "record.log")
	args := append([]string{"--model", "test-faux/faux-1", "--no-session", "-e", entry}, extra...)
	cmd := exec.CommandContext(t.Context(), buildPigBinaryForSignalTest(t), append(args, "--print", prompt)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "PIG_HOME="+t.TempDir(), "PIG_TEST_FAUX=1", logEnv+"="+log)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("pig --print: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "42") || stderr.Len() != 0 {
		t.Fatalf("the run did not complete cleanly; stdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// A print prompt's input that goes on to a run ends no wait of its own: the run beginning reaches every Node process as the host's next step, so the immediate the input handler queued runs during the run, as Pi's runs at the bash tool's I/O (agent-session.ts:1968-2000), and not after session_shutdown. extensions-runtime/74-print-input-run compares the record with Pi 1.1.0.
func TestPrintInputCallbackRunsDuringTheRun(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"text", "json"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			got := runPrintPromptEndFixture(t, "print-input-run.mjs", "PRINT_INPUT_RUN_LOG", "Run: expr 20 + 22", "--mode", mode)
			if want := "input Run: expr 20 + 22\ninput-immediate\nsession_shutdown\n"; got != want {
				t.Fatalf("record:\n got %q\nwant %q", got, want)
			}
		})
	}
}

// An input an extension sends with sendUserMessage during a print run ends no prompt: Pi's prompt() queues the follow-up and returns while the bash tool's child process runs (agent-session.ts:1968-2000,2365-2394), so the immediate the input handler queued runs before tool_result. extensions-runtime/74-print-midrun-input compares the record with Pi 1.1.0.
func TestPrintMidRunInputCallbackRunsBeforeTheRunsNextEvent(t *testing.T) {
	t.Parallel()
	got := runPrintPromptEndFixture(t, "print-midrun-input.mjs", "MIDRUN_INPUT_LOG", "Run: expr 20 + 22")
	if want := "tool_call bash\ninput What is 20+22?\ninput-immediate\ntool_result bash\nturn_end\nturn_end\nturn_end\n"; got != want {
		t.Fatalf("record:\n got %q\nwant %q", got, want)
	}
}
